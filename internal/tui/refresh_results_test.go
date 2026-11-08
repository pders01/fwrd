package tui

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

type refreshResultOps struct {
	recordingFeedOps
	refresh func(context.Context, bool) (feed.RefreshSummary, error)
	calls   atomic.Int32
}

func (o *refreshResultOps) RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error) {
	o.calls.Add(1)
	return o.refresh(ctx, force)
}

type refreshStats struct{ commandSearch }

func (*refreshStats) DocCount() (int, error) { return 42, nil }

// Execute only the finite commands returned by a refresh completion. A
// metadata reload must not replay RefreshAllFeeds or load/reset article lists.
func refreshMetadataMessage(t *testing.T, cmd tea.Cmd) feedsLoadedMsg {
	t.Helper()
	require.NotNil(t, cmd, "refresh completion must reload feed metadata")
	var messages []feedsLoadedMsg
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				run(child)
			}
		case feedsLoadedMsg:
			messages = append(messages, msg)
		default:
			t.Fatalf("unexpected refresh follow-up message %T: %#v", msg, msg)
		}
	}
	run(cmd)
	require.Len(t, messages, 1)
	return messages[0]
}

func TestRefreshResultsCountsSeverityAndMetadataReload(t *testing.T) {
	failed := errors.New("HTTP 504")
	for _, tc := range []struct {
		name    string
		summary feed.RefreshSummary
		err     error
		kind    StatusKind
	}{
		{"success", feed.RefreshSummary{UpdatedFeeds: 2, AddedArticles: 7}, nil, StatusSuccess},
		{"mixed", feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3, Errors: []error{failed}}, errors.Join(failed), StatusWarn},
		{"all failed", feed.RefreshSummary{Errors: []error{failed, failed}}, errors.Join(failed, failed), StatusError},
		{"summary without joined error", feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3, Errors: []error{failed}}, nil, StatusWarn},
		{"per-feed deadline", feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3, Errors: []error{context.DeadlineExceeded}}, fmt.Errorf("refresh: %w", errors.Join(context.DeadlineExceeded)), StatusWarn},
		{"per-feed cancellation", feed.RefreshSummary{Errors: []error{context.Canceled}}, errors.Join(context.Canceled), StatusError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := commandTestApp(t)
			app.searchEngine = &refreshStats{}
			app.SetForceRefresh(true)
			ops := &refreshResultOps{refresh: func(ctx context.Context, force bool) (feed.RefreshSummary, error) {
				require.NoError(t, ctx.Err())
				require.True(t, force)
				return tc.summary, tc.err
			}}
			app.feedOps = ops
			unrelated := errors.New("unrelated navigation error")
			app.err = unrelated
			app.startSpinner(MsgRefreshing)
			msg, ok := app.refreshFeeds()().(refreshDoneMsg)
			require.True(t, ok, "per-feed failures must preserve the refresh summary")
			require.Equal(t, tc.summary.UpdatedFeeds, msg.updatedFeeds)
			require.Equal(t, tc.summary.AddedArticles, msg.addedArticles)
			require.Equal(t, len(tc.summary.Errors), msg.errors)
			require.Equal(t, 42, msg.docCount)
			require.True(t, app.spinnerActive, "Cmd workers must not write live UI state")
			_, reload := app.Update(msg)
			require.False(t, app.spinnerActive)
			require.Empty(t, app.spinnerLabel)
			require.Equal(t, MsgRefreshSummary(msg.updatedFeeds, msg.addedArticles, msg.errors, 42), app.statusText)
			require.Equal(t, tc.kind, app.statusKind)
			require.True(t, app.statusUntil.After(time.Now()))
			require.ErrorIs(t, app.err, unrelated)
			app.Update(refreshMetadataMessage(t, reload))
			require.EqualValues(t, 1, ops.calls.Load(), "metadata reload must not replay mutations")
		})
	}
}

func TestRefreshResultsReloadBadgesAndSuccessfulChangesWithoutNavigation(t *testing.T) {
	app := commandTestApp(t)
	good := &storage.Feed{ID: "good", Title: "Old title", LastError: "old failure"}
	bad := &storage.Feed{ID: "bad", Title: "Bad feed"}
	require.NoError(t, app.store.SaveFeed(good))
	require.NoError(t, app.store.SaveFeed(bad))
	app.Update(app.loadFeeds()())
	article := &storage.Article{ID: "article", FeedID: good.ID}
	app.view, app.currentFeed, app.currentArticle = ViewArticles, good, article
	app.articles = []*storage.Article{article}
	app.articleList.SetItems([]list.Item{articleItem{article: article}})
	app.articlesSeq, app.articlesCursor, app.articlesHasMore = 9, "cursor", true
	failed := errors.New("HTTP 504")
	ops := &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
		// The operation owns persistence. The Cmd must only report results.
		fresh := *good
		fresh.Title, fresh.LastError, fresh.ETag = "Updated title", "", "new etag"
		require.NoError(t, app.store.SaveFeed(&fresh))
		fresh = *bad
		fresh.LastError, fresh.LastErrorAt = failed.Error(), time.Now()
		require.NoError(t, app.store.SaveFeed(&fresh))
		return feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 5, Errors: []error{failed}}, errors.Join(failed)
	}}
	app.feedOps = ops
	app.startSpinner(MsgRefreshing)
	msg := app.refreshFeeds()()
	for _, f := range app.feeds {
		if f.ID == good.ID {
			require.Equal(t, "Old title", f.Title, "worker must not alter live feed metadata")
		}
	}
	_, reload := app.Update(msg)
	app.Update(refreshMetadataMessage(t, reload))
	byID := make(map[string]*storage.Feed)
	for _, item := range app.feedList.Items() {
		f := item.(feedItem).feed
		byID[f.ID] = f
	}
	require.Equal(t, "Updated title", byID[good.ID].Title)
	require.Equal(t, "new etag", byID[good.ID].ETag)
	require.Empty(t, byID[good.ID].LastError)
	require.Equal(t, failed.Error(), byID[bad.ID].LastError)
	require.False(t, byID[bad.ID].LastErrorAt.IsZero())
	require.Contains(t, (feedItem{feed: byID[bad.ID]}).Title(), "fetch failed")
	require.Equal(t, ViewArticles, app.view)
	require.Same(t, good, app.currentFeed)
	require.Same(t, article, app.currentArticle)
	require.Same(t, article, app.articles[0])
	require.Same(t, article, app.articleList.Items()[0].(articleItem).article)
	require.EqualValues(t, 9, app.articlesSeq)
	require.Equal(t, "cursor", app.articlesCursor)
	require.True(t, app.articlesHasMore)
	require.EqualValues(t, 1, ops.calls.Load())
}

func TestRefreshResultsTopLevelAndSessionFailures(t *testing.T) {
	topLevel := errors.New("RPC unavailable")
	perFeed := errors.New("HTTP 504")
	for _, tc := range []struct {
		name    string
		ctx     func() (context.Context, context.CancelFunc)
		summary feed.RefreshSummary
		err     error
		want    error
	}{
		{"top level", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, feed.RefreshSummary{}, topLevel, topLevel},
		{"top level after successful updates", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3}, topLevel, topLevel},
		{"top level with feed failures", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, feed.RefreshSummary{Errors: []error{perFeed}}, topLevel, topLevel},
		{"joined top level with feed failures", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, feed.RefreshSummary{Errors: []error{perFeed}}, fmt.Errorf("refresh: %w", errors.Join(perFeed, topLevel)), topLevel},
		{"operation cancellation with live session", func() (context.Context, context.CancelFunc) { return context.WithCancel(context.Background()) }, feed.RefreshSummary{Errors: []error{perFeed}}, errors.Join(perFeed, context.Canceled), context.Canceled},
		{"canceled with partial result", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3, Errors: []error{perFeed}}, errors.Join(perFeed, context.Canceled), context.Canceled},
		{"deadline with partial result", func() (context.Context, context.CancelFunc) {
			return context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		}, feed.RefreshSummary{Errors: []error{perFeed}}, errors.Join(perFeed, context.DeadlineExceeded), context.DeadlineExceeded},
		{"canceled without operation error", func() (context.Context, context.CancelFunc) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			return ctx, cancel
		}, feed.RefreshSummary{UpdatedFeeds: 1}, nil, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := commandTestApp(t)
			ctx, cancel := tc.ctx()
			defer cancel()
			app.commandCtx = ctx
			app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
				return tc.summary, tc.err
			}}
			app.startSpinner(MsgRefreshing)
			msg, ok := app.refreshFeeds()().(errorMsg)
			require.True(t, ok, "genuine operation failures must not become successful summaries")
			require.ErrorIs(t, msg.err, tc.want)
			if tc.err != nil {
				require.ErrorIs(t, msg.err, tc.err, "context cancellation must not drop the operation error")
			}
			_, reload := app.Update(msg)
			app.Update(refreshMetadataMessage(t, reload))
			require.ErrorIs(t, app.err, tc.want)
			require.False(t, app.spinnerActive)
			require.Equal(t, StatusError, app.statusKind)
		})
	}
}

func TestRefreshResultsReconcileStaleCompletionsWithoutPresentingThem(t *testing.T) {
	app := commandTestApp(t)
	app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
		return feed.RefreshSummary{UpdatedFeeds: 1}, nil
	}}
	old := app.refreshFeeds()()
	newer := app.refreshFeeds()()
	app.startSpinner(MsgRefreshing)
	_, cmd := app.Update(old)
	app.Update(refreshMetadataMessage(t, cmd))
	require.True(t, app.spinnerActive)
	require.Empty(t, app.statusText)
	_, cmd = app.Update(errorMsg{err: errors.New("late RPC failure"), refreshSeq: old.(refreshDoneMsg).seq})
	app.Update(refreshMetadataMessage(t, cmd))
	require.True(t, app.spinnerActive)
	require.Empty(t, app.statusText)
	require.NoError(t, app.err)
	_, cmd = app.Update(newer)
	app.Update(refreshMetadataMessage(t, cmd))
	require.False(t, app.spinnerActive)
	require.Equal(t, StatusSuccess, app.statusKind)
}

func TestRefreshResultsWorkersCaptureInputsWithoutModelWrites(t *testing.T) {
	app := commandTestApp(t)
	started, release := make(chan struct{}), make(chan struct{})
	capturedForce := make(chan bool, 1)
	ops := &refreshResultOps{refresh: func(_ context.Context, force bool) (feed.RefreshSummary, error) {
		close(started)
		<-release
		capturedForce <- force
		return feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 2}, nil
	}}
	app.feedOps, app.searchEngine = ops, &refreshStats{}
	app.SetForceRefresh(true)
	app.startSpinner(MsgRefreshing)
	cmd := app.refreshFeeds()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	// These fields belong to Update; a running worker must use its snapshot.
	app.SetForceRefresh(false)
	app.feedOps, app.searchEngine = nil, &commandSearch{}
	app.view = ViewReader
	app.startSpinner(MsgLoadingArticle)
	app.Update(tea.WindowSizeMsg{Width: 90, Height: 30})
	close(release)
	msg := (<-result).(refreshDoneMsg)
	require.True(t, <-capturedForce)
	require.Equal(t, 42, msg.docCount)
	require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
	require.Empty(t, app.statusText, "worker must leave presentation changes to Update")
	require.EqualValues(t, 1, ops.calls.Load())
	_, reload := app.Update(msg)
	app.Update(refreshMetadataMessage(t, reload))
	require.Equal(t, ViewReader, app.view)
	require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
}

func TestRefreshResultsPreserveReaderSpinnerAndShutdown(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			app := commandTestApp(t)
			app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
				if failed {
					return feed.RefreshSummary{}, errors.New("RPC unavailable")
				}
				return feed.RefreshSummary{UpdatedFeeds: 1}, nil
			}}
			cmd := app.refreshFeeds()
			app.view, app.loadingArticle = ViewReader, true
			app.startSpinner(MsgLoadingArticle)
			app.Update(cmd())
			require.Equal(t, ViewReader, app.view)
			require.True(t, app.loadingArticle)
			require.True(t, app.spinnerActive)
			require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
			late := app.refreshFeeds()
			app.Close()
			require.Nil(t, late(), "unexecuted commands must not use closed resources")
			_, followup := app.Update(refreshDoneMsg{seq: app.refreshSeq, updatedFeeds: 99})
			require.Nil(t, followup, "closed models must not schedule metadata reloads")
		})
	}
}
