package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/control"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/terminaltext"
	"github.com/stretchr/testify/require"
)

type commandSearch struct {
	global func(string) ([]*search.Result, error)
	local  func(*storage.Article, string) ([]*search.Result, error)
	closes atomic.Int32
}

func (s *commandSearch) Search(query string, _ int) ([]*search.Result, error) {
	if s.global != nil {
		return s.global(query)
	}
	return nil, nil
}

func (s *commandSearch) SearchInArticle(article *storage.Article, query string) ([]*search.Result, error) {
	if s.local != nil {
		return s.local(article, query)
	}
	return nil, nil
}

func (s *commandSearch) Close() error { s.closes.Add(1); return nil }

func commandTestApp(t *testing.T) *App {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })
	cfg := config.TestConfig()
	cfg.UI.Theme = "dark"
	app := newApp(store, cfg, nil, nil, true)
	app.width, app.height = 100, 30
	t.Cleanup(app.Close)
	return app
}

// All commands start in separate goroutines behind one barrier. Returned
// messages are delivered by the test on the Update goroutine, in chosen order.
func overlapCommands(cmds ...tea.Cmd) []tea.Msg {
	messages := make([]tea.Msg, len(cmds))
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i, cmd := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			messages[i] = cmd()
		}()
	}
	close(start)
	wg.Wait()
	return messages
}

func TestOverlappingReadStarIntentsComposeWithoutWorkerModelWrites(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", FeedID: "f", Title: "Article"}
	require.NoError(t, app.store.SaveArticles([]*storage.Article{article}))
	app.articles = []*storage.Article{article}
	app.currentArticle = article
	app.articleList.SetItems([]list.Item{articleItem{article: article}})
	searchCopy := *article
	app.searchResults = []searchResultItem{{article: &searchCopy, isArticle: true}}
	app.searchList.SetItems([]list.Item{app.searchResults[0]})

	cmds := []tea.Cmd{
		app.toggleRead(article),      // true
		app.toggleRead(article),      // false, even before the first completion
		app.markArticleRead(article), // true
		app.toggleRead(article),      // false: opening must not undo a later toggle
		app.toggleStarred(article),
		app.toggleStarred(article),
		app.toggleStarred(article), // true
	}
	messages := overlapCommands(cmds...)
	// Workers may persist state, but never change live model pointers.
	require.False(t, article.Read)
	require.False(t, article.Starred)
	for i := len(messages) - 1; i >= 0; i-- {
		app.Update(messages[i])
	}
	saved, err := app.store.GetArticle(article.ID)
	require.NoError(t, err)
	require.False(t, saved.Read)
	require.True(t, saved.Starred)
	require.Equal(t, saved.Read, article.Read)
	require.Equal(t, saved.Starred, article.Starred)
	require.True(t, searchCopy.Starred)
	require.NoError(t, app.err)
}

func TestReadIntentRejectsLateSuccessfulCompletionAndFailure(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", FeedID: "f"}
	require.NoError(t, app.store.SaveArticles([]*storage.Article{article}))
	app.currentArticle = article
	old := app.toggleRead(article)()
	newer := app.toggleRead(article)()
	app.Update(newer)
	app.Update(old)
	oldMsg := old.(articleReadToggledMsg)
	oldMsg.err = errors.New("stale write failure")
	app.Update(oldMsg)
	require.False(t, article.Read)
	require.NoError(t, app.err)
	saved, err := app.store.GetArticle(article.ID)
	require.NoError(t, err)
	require.False(t, saved.Read)
}

func TestCompletedIntentsRespectReloadedReaderState(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", FeedID: "f"}
	require.NoError(t, app.store.SaveArticles([]*storage.Article{article}))
	app.currentArticle = article
	app.Update(app.toggleRead(article)())
	require.True(t, article.Read)
	// A different client changes state and the model receives a fresh record.
	require.NoError(t, app.store.MarkArticleRead(article.ID, false))
	reloaded, err := app.store.GetArticle(article.ID)
	require.NoError(t, err)
	app.currentArticle = reloaded
	app.Update(app.toggleRead(reloaded)())
	require.True(t, reloaded.Read, "completed intent history must not override freshly loaded state")
}

func TestOverlappingSearchCompletionsAndErrorsAreGenerationBound(t *testing.T) {
	for _, failOld := range []bool{false, true} {
		t.Run(fmt.Sprint(failOld), func(t *testing.T) {
			app := commandTestApp(t)
			app.view = ViewSearch
			oldStarted, newStarted, releaseOld := make(chan struct{}), make(chan struct{}), make(chan struct{})
			app.searchEngine = &commandSearch{global: func(query string) ([]*search.Result, error) {
				if query == "old" {
					close(oldStarted)
					<-releaseOld
					if failOld {
						return nil, errors.New("stale search failure")
					}
				} else {
					close(newStarted)
				}
				return []*search.Result{{Feed: &storage.Feed{ID: query, Title: query}}}, nil
			}}
			oldCmd := app.performSearch("old")
			oldResult := make(chan tea.Msg, 1)
			go func() { oldResult <- oldCmd() }()
			<-oldStarted
			newCmd := app.performSearch("new")
			newResult := make(chan tea.Msg, 1)
			go func() { newResult <- newCmd() }()
			<-newStarted
			app.Update(<-newResult)
			close(releaseOld)
			app.Update(<-oldResult)
			require.Len(t, app.searchResults, 1)
			require.Equal(t, "new", app.searchResults[0].feed.ID)
			require.NoError(t, app.err)
		})
	}
}

func TestSearchContextSnapshotsAndTargetIdentity(t *testing.T) {
	app := commandTestApp(t)
	app.view = ViewSearch
	article := &storage.Article{ID: "first", Title: "original", MediaURLs: []string{"original"}}
	app.currentArticle = article
	started, release := make(chan struct{}), make(chan struct{})
	captured := make(chan *storage.Article, 1)
	app.searchEngine = &commandSearch{local: func(snapshot *storage.Article, _ string) ([]*search.Result, error) {
		close(started)
		<-release
		captured <- snapshot
		return []*search.Result{{Article: snapshot, IsArticle: true}}, nil
	}}
	cmd := app.performSearchWithContext("query", "article")
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	article.Title = "changed on Update"
	article.MediaURLs[0] = "changed"
	app.currentArticle = &storage.Article{ID: "second"}
	close(release)
	app.Update(<-result)
	snapshot := <-captured
	require.Equal(t, "original", snapshot.Title)
	require.Equal(t, []string{"original"}, snapshot.MediaURLs)
	require.Empty(t, app.searchResults, "result belongs to another article")
}

func TestSearchLeavingAndReenteringInvalidatesPendingResults(t *testing.T) {
	app := commandTestApp(t)
	app.searchEngine = &commandSearch{global: func(string) ([]*search.Result, error) {
		return nil, errors.New("old session failure")
	}}
	app.view = ViewSearch
	app.previousView = ViewFeeds
	cmd := app.performSearch("query")
	app.keyHandler.navigateBack()
	app.keyHandler.enterSearchMode()
	app.Update(cmd())
	require.NoError(t, app.err)
	require.Empty(t, app.searchResults)
}

func TestOverlappingRendererCommandsOwnRendererAndSnapshots(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", Title: "original\x1b]52;c;evil\a title", Content: strings.Repeat("original reader text\x1b[2J\n\n", 500), MediaURLs: []string{"https://example.test/original"}}
	app.currentArticle, app.view, app.loadingArticle = article, ViewReader, true
	const n = 12
	cmds := make([]tea.Cmd, n)
	for i := range cmds {
		cmds[i] = app.renderArticle(article)
	}
	start, done := make(chan struct{}), make(chan []tea.Msg, 1)
	go func() {
		close(start)
		done <- overlapCommands(cmds...)
	}()
	<-start
	// Model updates run while Cmd goroutines render detached snapshots.
	for range 100 {
		article.Title = "changed after scheduling"
		article.MediaURLs[0] = "changed"
		app.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	}
	messages := <-done
	latest := messages[n-1].(articleRenderedMsg)
	require.NoError(t, latest.err)
	plain := terminaltext.Safe(latest.content)
	require.Contains(t, plain, "original")
	require.NotContains(t, plain, "changed after")
	require.NotContains(t, latest.content, "\x1b]52")
	require.NotContains(t, latest.content, "\x1b[2J")
	require.Contains(t, latest.content, "\x1b[", "Glamour's intended styling must survive input sanitization")
	app.Update(latest)
	accepted := app.viewport.View()
	for _, message := range messages[:n-1] {
		app.Update(message)
	}
	require.Equal(t, accepted, app.viewport.View())
	require.False(t, app.loadingArticle)
}

func TestRendererRejectsStaleErrorsAndDifferentTargets(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", Title: "fresh", Content: "body"}
	app.currentArticle, app.view, app.loadingArticle = article, ViewReader, true
	app.glamourStyle = "nonexistent-style-for-test"
	old := app.renderArticle(article)
	app.glamourStyle = "dark"
	newer := app.renderArticle(article)
	messages := overlapCommands(old, newer)
	require.Error(t, messages[0].(articleRenderedMsg).err)
	app.Update(messages[1])
	app.Update(messages[0])
	require.NoError(t, app.err)
	require.False(t, app.loadingArticle)
	accepted := app.viewport.View()
	wrong := messages[1].(articleRenderedMsg)
	wrong.articleID = "other"
	wrong.content = "wrong target"
	app.Update(wrong)
	require.Equal(t, accepted, app.viewport.View())
}

func TestRequestRecoveryClearsOnlyItsOwnError(t *testing.T) {
	app := commandTestApp(t)
	app.view = ViewSearch
	searchFailure := errors.New("search failed")
	app.searchEngine = &commandSearch{global: func(query string) ([]*search.Result, error) {
		if query == "fail" {
			return nil, searchFailure
		}
		return nil, nil
	}}
	app.Update(app.performSearch("fail")())
	require.ErrorIs(t, app.err, searchFailure)
	app.Update(app.performSearch("recover")())
	require.NoError(t, app.err)
	app.Update(app.performSearch("fail")())
	unrelated := errors.New("unrelated operation")
	app.err = unrelated
	app.Update(app.performSearch("recover")())
	require.ErrorIs(t, app.err, unrelated)

	app.currentArticle = &storage.Article{ID: "a"}
	app.view, app.renderSeq = ViewReader, 1
	renderFailure := errors.New("render failed")
	app.Update(articleRenderedMsg{articleID: "a", seq: 1, err: renderFailure})
	require.ErrorIs(t, app.err, renderFailure)
	app.renderSeq++
	app.Update(articleRenderedMsg{articleID: "a", seq: 2, content: "recovered"})
	require.NoError(t, app.err)
}

func TestCloseCancelsThemeReaderDrainsWorkersAndRejectsLateCommands(t *testing.T) {
	app := commandTestApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	engine := &commandSearch{global: func(string) ([]*search.Result, error) {
		close(started)
		<-release
		return nil, nil
	}}
	app.searchEngine, app.ownsSearch = engine, true
	cmd, late := app.performSearch("running"), app.performSearch("not executed")
	reader := app.waitThemeChange()
	readerDone, cmdDone := make(chan tea.Msg, 1), make(chan tea.Msg, 1)
	go func() { readerDone <- reader() }()
	go func() { cmdDone <- cmd() }()
	<-started
	closed := make(chan struct{})
	go func() { app.Close(); close(closed) }()
	select {
	case <-readerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("theme reader did not observe shutdown")
	}
	require.Zero(t, engine.closes.Load(), "index must remain open while commands own it")
	select {
	case <-closed:
		t.Fatal("Close returned before a running command finished")
	default:
	}
	close(release)
	<-cmdDone
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close failed to drain commands")
	}
	require.Nil(t, late(), "unexecuted commands must not access closed resources")
	app.Close()
	require.EqualValues(t, 1, engine.closes.Load())
}

func TestAppCloseReleasesOwnedBleveIndexForReuse(t *testing.T) {
	app := commandTestApp(t)
	path := filepath.Join(t.TempDir(), "index.bleve")
	engine, err := search.NewBleveEngine(app.store, path)
	require.NoError(t, err)
	app.searchEngine, app.ownsSearch = engine, true
	late := app.performSearch("query")
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); app.Close() }()
	}
	wg.Wait()
	require.Nil(t, late())
	reopened, err := search.NewBleveEngine(app.store, path)
	require.NoError(t, err, "App.Close must release the on-disk Bleve lock")
	require.NoError(t, reopened.(interface{ Close() error }).Close())
}

type serialFeedOps struct {
	recordingFeedOps
	entered chan string
	release chan struct{}
	active  atomic.Int32
	peak    atomic.Int32
}

func (o *serialFeedOps) hold(ctx context.Context, name string) error {
	active := o.active.Add(1)
	defer o.active.Add(-1)
	for {
		peak := o.peak.Load()
		if active <= peak || o.peak.CompareAndSwap(peak, active) {
			break
		}
	}
	o.entered <- name
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-o.release:
		return nil
	}
}

func (o *serialFeedOps) RefreshAllFeeds(ctx context.Context, _ bool) (feed.RefreshSummary, error) {
	return feed.RefreshSummary{}, o.hold(ctx, "refresh")
}

func (o *serialFeedOps) DeleteFeed(ctx context.Context, _ string) error {
	return o.hold(ctx, "delete")
}

func TestOverlappingFeedCommandsShareMutationCoordinator(t *testing.T) {
	app := commandTestApp(t)
	ops := &serialFeedOps{entered: make(chan string, 2), release: make(chan struct{})}
	defer close(ops.release)
	app.feedOps = control.NewMutationCoordinator(ops)
	refresh, deleteFeed := app.refreshFeeds(), app.deleteFeed("feed")
	results := make(chan tea.Msg, 2)
	go func() { results <- refresh() }()
	require.Equal(t, "refresh", <-ops.entered)
	attempting := make(chan struct{})
	go func() { close(attempting); results <- deleteFeed() }()
	<-attempting
	select {
	case operation := <-ops.entered:
		t.Fatalf("%s overlapped an active refresh", operation)
	case <-time.After(30 * time.Millisecond):
	}
	ops.release <- struct{}{}
	require.Equal(t, "delete", <-ops.entered)
	ops.release <- struct{}{}
	<-results
	<-results
	require.EqualValues(t, 1, ops.peak.Load())
}

func TestCloseCancelsSessionOperationsWithoutClosingRemoteEngine(t *testing.T) {
	app := commandTestApp(t)
	ops := &serialFeedOps{entered: make(chan string, 1), release: make(chan struct{})}
	app.feedOps = ops // remote operations are deliberately not rewrapped
	engine := &commandSearch{}
	app.searchEngine = engine
	cmd := app.refreshFeeds()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-ops.entered
	closed := make(chan struct{})
	go func() { app.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the session operation")
	}
	require.ErrorIs(t, (<-result).(errorMsg).err, context.Canceled)
	require.Zero(t, engine.closes.Load(), "the remote session's resources are not App-owned")
}

func TestLocalConstructorsCoordinateMutations(t *testing.T) {
	cfg := config.TestConfig()
	for _, construct := range []func() *App{
		func() *App { return NewApp(nil, cfg) },
		func() *App { return NewAppWithSession(&control.Session{}, cfg) },
	} {
		app := construct()
		_, ok := app.feedOps.(*control.MutationCoordinator)
		require.True(t, ok)
		app.Close()
	}
}
