package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/terminaltext"
	"github.com/stretchr/testify/require"
)

func navigationKey(a *App, key tea.KeyType) tea.Cmd {
	_, cmd := a.Update(tea.KeyMsg{Type: key})
	return cmd
}

// Execute finite navigation commands only; never follow spinner subscriptions.
func finishNavigation(t *testing.T, a *App, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, child := range msg {
			finishNavigation(t, a, child)
		}
	case spinner.TickMsg:
	case articleRenderedMsg, articleReadToggledMsg, articleStarToggledMsg, articlesLoadedMsg:
		a.Update(msg)
	default:
		t.Fatalf("unexpected navigation command: %T", msg)
	}
}

func chooseNavigationHit(a *App, hit searchResultItem) tea.Cmd {
	a.searchResults = []searchResultItem{hit}
	a.searchList.SetItems([]list.Item{hit})
	return navigationKey(a, tea.KeyEnter) // exercises selectSearchResult via HandleKey
}

func navigationArticle(id string) *storage.Article {
	return &storage.Article{ID: id, FeedID: "feed-" + id, Title: "Article " + id,
		URL: "https://example.test/" + id, Content: strings.Repeat("body "+id+"\n\n", 100),
		MediaURLs: []string{"https://example.test/" + id + "/1.mp3", "https://example.test/" + id + "/2.mp3"}}
}

func TestSearchSelectionRestoresSuspendedReaderAndChooser(t *testing.T) {
	for _, originMedia := range []bool{false, true} {
		for _, hitMedia := range []bool{false, true} {
			for _, initial := range []bool{false, true} {
				t.Run(fmt.Sprintf("originMedia=%v/hitMedia=%v/initial=%v", originMedia, hitMedia, initial), func(t *testing.T) {
					a := commandTestApp(t)
					articleA, articleB, articleC := navigationArticle("A"), navigationArticle("B"), navigationArticle("C")
					require.NoError(t, a.store.SaveArticles([]*storage.Article{articleA, articleB, articleC}))
					feedA, feedB := &storage.Feed{ID: articleA.FeedID}, &storage.Feed{ID: articleB.FeedID}
					a.currentArticle, a.currentFeed, a.view = articleA, feedA, ViewReader
					a.articles = []*storage.Article{articleA}
					a.articleList.SetItems([]list.Item{articleItem{article: articleA}})
					a.viewport.Width, a.viewport.Height = 100, 22
					a.viewport.SetContent(strings.Repeat("suspended A\n", 100))
					a.viewport.SetYOffset(17)
					for cycle := range 2 {
						a.loadingArticle = initial && cycle == 0
						oldA := a.renderArticle(articleA)
						if originMedia {
							navigationKey(a, tea.KeyCtrlO)
							a.mediaList.Select(2)
						}
						chooserTitle := a.mediaList.Title
						navigationKey(a, tea.KeyCtrlS)
						finishNavigation(t, a, chooseNavigationHit(a, searchResultItem{isArticle: true, article: articleB, feed: feedB}))
						a.viewport.SetYOffset(8)
						if hitMedia {
							navigationKey(a, tea.KeyCtrlO)
							a.mediaList.Select(1)
						}
						// A second search and a third distinct reader must not consume
						// the original frame or its chooser.
						navigationKey(a, tea.KeyCtrlS)
						finishNavigation(t, a, chooseNavigationHit(a, searchResultItem{isArticle: true, article: articleC}))
						navigationKey(a, tea.KeyCtrlO)
						navigationKey(a, tea.KeyEsc) // media C -> C
						navigationKey(a, tea.KeyEsc) // C -> inner search
						require.Same(t, articleB, a.searchContextArticle())
						require.Equal(t, 8, a.viewport.YOffset)
						finishNavigation(t, a, navigationKey(a, tea.KeyEsc)) // -> B / media B
						if hitMedia {
							require.Equal(t, ViewMedia, a.view)
							require.Equal(t, articleB.MediaURLs[0], a.mediaList.SelectedItem().(mediaItem).url)
							navigationKey(a, tea.KeyEsc)
						}
						navigationKey(a, tea.KeyEsc) // B -> outer search
						require.Equal(t, ViewSearch, a.view)
						require.Same(t, articleA, a.currentArticle)
						require.Same(t, feedA, a.currentFeed)
						require.Same(t, articleA, a.searchContextArticle())
						require.Equal(t, "B", a.searchList.Items()[0].(searchResultItem).article.ID)
						// Debounced contextual search must use A, never B or C.
						a.searchEngine = &commandSearch{local: func(target *storage.Article, _ string) ([]*search.Result, error) {
							require.Equal(t, "A", target.ID)
							return nil, nil
						}}
						a.pendingSearchQuery = "body"
						_, searchCmd := a.Update(searchDebounceFireMsg{seq: a.searchSeq})
						require.NotNil(t, searchCmd)
						searchMsg := searchCmd().(searchResultsMsg)
						require.Equal(t, "A", searchMsg.articleID)
						a.Update(searchMsg)
						_, resume := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
						seq := a.renderSeq
						a.Update(oldA())
						require.Equal(t, seq, a.renderSeq)
						finishNavigation(t, a, resume)
						require.False(t, a.loadingArticle)
						require.False(t, a.readerInitialPending)
						require.False(t, a.spinnerActive)
						require.Empty(t, a.searchContexts)
						if originMedia {
							require.Equal(t, ViewMedia, a.view)
							require.Equal(t, chooserTitle, a.mediaList.Title)
							require.Equal(t, articleA.MediaURLs[1], a.mediaList.SelectedItem().(mediaItem).url)
							require.Len(t, a.mediaList.Items(), 3)
							navigationKey(a, tea.KeyEsc)
						}
						require.Equal(t, ViewReader, a.view)
						require.Same(t, articleA, a.currentArticle)
						require.NotContains(t, a.View(), MsgLoadingArticle)
						if initial {
							require.Zero(t, a.viewport.YOffset)
						} else {
							require.Equal(t, 17, a.viewport.YOffset)
						}
						require.Contains(t, terminaltext.Safe(a.viewport.View()), "body A")
					}
					navigationKey(a, tea.KeyEsc)
					require.Equal(t, ViewArticles, a.view)
					require.Same(t, feedA, a.currentFeed)
					require.Same(t, articleA, a.articleList.Items()[0].(articleItem).article)
				})
			}
		}
	}
}

func TestSearchSelectionLateRenderCannotOrphanResumedInitialReader(t *testing.T) {
	a := commandTestApp(t)
	oldA := prepareReader(t, a, true)
	articleA := a.currentArticle
	navigationKey(a, tea.KeyCtrlO)
	navigationKey(a, tea.KeyCtrlS)
	articleB := navigationArticle("B")
	require.NoError(t, a.store.SaveArticles([]*storage.Article{articleB}))
	openB := chooseNavigationHit(a, searchResultItem{isArticle: true, article: articleB})
	navigationKey(a, tea.KeyCtrlO)
	navigationKey(a, tea.KeyEsc) // media B -> B, whose render is still pending
	navigationKey(a, tea.KeyEsc) // B -> search over A
	require.False(t, a.loadingArticle)
	require.True(t, a.readerInitialPending)
	a.startSpinner(MsgRefreshing)
	resume := navigationKey(a, tea.KeyEsc)
	require.True(t, a.loadingArticle)
	require.Same(t, articleA, a.currentArticle)
	before := a.viewport.View()
	finishNavigation(t, a, openB) // B completion must not end A's load
	a.Update(oldA())
	require.Equal(t, before, a.viewport.View())
	require.True(t, a.loadingArticle)
	require.Equal(t, MsgRefreshing, a.spinnerLabel)
	finishNavigation(t, a, resume)
	require.False(t, a.loadingArticle)
	require.False(t, a.readerInitialPending)
	require.Zero(t, a.viewport.YOffset)
	require.True(t, a.spinnerActive)
	require.Equal(t, MsgRefreshing, a.spinnerLabel)
	require.Len(t, a.mediaList.Items(), 2)
	navigationKey(a, tea.KeyEsc)
	require.Equal(t, ViewReader, a.view)
	require.Contains(t, terminaltext.Safe(a.viewport.View()), "Target")
}

func TestSearchFeedHitRestoresReaderOriginAndPendingIntents(t *testing.T) {
	a := commandTestApp(t)
	articleA, articleB := navigationArticle("A"), navigationArticle("B")
	require.NoError(t, a.store.SaveArticles([]*storage.Article{articleA, articleB}))
	a.currentArticle, a.currentFeed, a.view = articleA, &storage.Feed{ID: articleA.FeedID}, ViewReader
	a.viewport.SetContent(strings.Repeat("A\n", 100))
	a.viewport.SetYOffset(13)
	read, star := a.markArticleRead(articleA), a.toggleStarred(articleA)
	navigationKey(a, tea.KeyCtrlO)
	navigationKey(a, tea.KeyCtrlS)
	finishNavigation(t, a, chooseNavigationHit(a, searchResultItem{feed: &storage.Feed{ID: articleB.FeedID}}))
	require.Equal(t, ViewArticles, a.view)
	// Intent completions arrive while A is present only in the suspended frame.
	a.currentArticle = articleB
	a.Update(read())
	a.Update(star())
	require.True(t, a.searchContexts[0].origin.article.Read)
	require.True(t, a.searchContexts[0].origin.article.Starred)
	navigationKey(a, tea.KeyCtrlS) // repeated search over the feed result
	require.Nil(t, a.searchContextArticle(), "article-list searches remain global")
	finishNavigation(t, a, navigationKey(a, tea.KeyEsc))
	require.Equal(t, ViewArticles, a.view)
	navigationKey(a, tea.KeyEsc)
	require.Equal(t, ViewSearch, a.view)
	require.Same(t, articleA, a.searchContextArticle())
	finishNavigation(t, a, navigationKey(a, tea.KeyEsc))
	require.Equal(t, ViewMedia, a.view)
	require.Len(t, a.mediaList.Items(), 3)
	require.True(t, a.currentArticle.Read)
	require.True(t, a.currentArticle.Starred)
	require.Equal(t, 13, a.viewport.YOffset)
	navigationKey(a, tea.KeyEsc)
	require.Equal(t, ViewReader, a.view)
}

func TestThemeChangeReplacesRenderBeneathMedia(t *testing.T) {
	for _, initial := range []bool{false, true} {
		t.Run(fmt.Sprint(initial), func(t *testing.T) {
			a := commandTestApp(t)
			oldCmd := prepareReader(t, a, initial)
			oldRenderer := a.glamourRenderer
			navigationKey(a, tea.KeyCtrlO)
			a.startSpinner(MsgRefreshing)
			a.themePref = ThemePrefLight
			// Make the re-armed watcher finite without launching a watcher.
			a.signalThemeChange()
			_, cmd := a.Update(themeChangedMsg{})
			require.NotSame(t, oldRenderer, a.glamourRenderer)
			old := oldCmd().(articleRenderedMsg)
			before := a.viewport.View()
			a.Update(old)
			require.Equal(t, before, a.viewport.View())
			require.Equal(t, initial, a.loadingArticle)
			var fresh articleRenderedMsg
			for _, child := range cmd().(tea.BatchMsg) {
				switch msg := child().(type) {
				case articleRenderedMsg:
					fresh = msg
				case themeChangedMsg, spinner.TickMsg:
				default:
					t.Fatalf("unexpected theme command %T", msg)
				}
			}
			require.Greater(t, fresh.seq, old.seq)
			require.NoError(t, fresh.err)
			require.NotEqual(t, old.content, fresh.content)
			a.Update(fresh)
			require.False(t, a.loadingArticle)
			require.Equal(t, MsgRefreshing, a.spinnerLabel)
			require.True(t, a.spinnerActive)
			navigationKey(a, tea.KeyEsc)
			require.Equal(t, ViewReader, a.view)
			if initial {
				require.Zero(t, a.viewport.YOffset)
			} else {
				require.Equal(t, 12, a.viewport.YOffset)
			}
			if initial {
				require.Contains(t, terminaltext.Safe(a.viewport.View()), "Target")
			} else {
				require.Contains(t, terminaltext.Safe(a.viewport.View()), "reader body")
			}
		})
	}
}
