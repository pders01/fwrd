package tui

import (
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
)

// Search suspends navigation, not the App. In particular generations, errors,
// renderers, intent lanes, spinners and worker/shutdown state are never restored.
// Widget values here belong exclusively to the Update goroutine; commands only
// receive detached article snapshots.
type searchNavigation struct {
	origin  navigationContext
	input   textinput.Model
	results []searchResultItem
	list    list.Model
	query   string
}

type navigationContext struct {
	view, previousView, searchReturnContext View
	cameFromSearch                          bool
	articlesOrigin                          View
	feed                                    *storage.Feed
	article                                 *storage.Article
	viewport                                viewport.Model
	initialPending                          bool
	mediaPreviousView                       View
	mediaURLs                               []string
	mediaList                               list.Model
	articles                                []*storage.Article
	articleList                             list.Model
	articlesCursor                          string
	articlesHasMore, articlesLoadingMore    bool
}

func (a *App) suspendSearchNavigation() {
	a.searchContexts = append(a.searchContexts, searchNavigation{origin: navigationContext{
		view: a.view, previousView: a.previousView, searchReturnContext: a.searchReturnContext,
		cameFromSearch: a.cameFromSearch, articlesOrigin: a.articlesOrigin,
		feed: a.currentFeed, article: a.currentArticle, viewport: a.viewport,
		initialPending:    a.readerInitialPending,
		mediaPreviousView: a.mediaPreviousView, mediaURLs: a.mediaURLs, mediaList: a.mediaList,
		articles: a.articles, articleList: a.articleList, articlesCursor: a.articlesCursor,
		articlesHasMore: a.articlesHasMore, articlesLoadingMore: a.articlesLoadingMore,
	}})
}

func (a *App) saveSearchResults() {
	if len(a.searchContexts) == 0 {
		return
	}
	s := &a.searchContexts[len(a.searchContexts)-1]
	s.input, s.results, s.list, s.query = a.searchInput, a.searchResults, a.searchList, a.pendingSearchQuery
}

func (a *App) restoreNavigation(n *navigationContext) {
	// Never revive a generation whose completion may already have been ignored.
	a.renderSeq++
	a.articlesSeq++
	a.releaseReaderLoading()
	a.view, a.previousView, a.searchReturnContext = n.view, n.previousView, n.searchReturnContext
	a.cameFromSearch, a.articlesOrigin = n.cameFromSearch, n.articlesOrigin
	a.currentFeed, a.currentArticle, a.viewport = n.feed, n.article, n.viewport
	// Dimensions belong to the current terminal, not the suspended navigation.
	a.viewport.Width, a.viewport.Height = a.width, a.height-viewportChrome
	a.readerInitialPending = n.initialPending
	a.mediaPreviousView, a.mediaURLs, a.mediaList = n.mediaPreviousView, n.mediaURLs, n.mediaList
	a.mediaList.SetSize(a.width, a.height-viewportChrome)
	a.articles, a.articleList, a.articlesCursor = n.articles, n.articleList, n.articlesCursor
	a.articleList.SetSize(a.width, a.height-listViewChrome)
	a.articlesHasMore, a.articlesLoadingMore = n.articlesHasMore, false
}

// Returning from a hit restores the origin now, so subsequent searches and the
// search header target A rather than the last selected article B. The frame is
// retained until search itself is dismissed.
func (a *App) returnToSearch() {
	if len(a.searchContexts) > 0 {
		s := &a.searchContexts[len(a.searchContexts)-1]
		a.restoreNavigation(&s.origin)
		a.previousView, a.searchReturnContext = s.origin.view, s.origin.previousView
		a.searchInput, a.searchResults, a.searchList, a.pendingSearchQuery = s.input, s.results, s.list, s.query
		a.searchList.SetSize(a.width, max(a.height-searchViewChrome, minSearchListHeight))
	}
	a.searchSeq++
	a.view = ViewSearch
	a.searchInput.Blur()
}

func (a *App) leaveSearch() tea.Cmd {
	a.searchSeq++
	a.searchInput.Reset()
	a.searchResults = nil
	a.searchList.SetItems(nil)
	a.pendingSearchQuery = ""
	if len(a.searchContexts) == 0 {
		// Keep compatibility with independently constructed search views.
		a.view, a.previousView = a.previousView, a.searchReturnContext
	} else {
		i := len(a.searchContexts) - 1
		n := a.searchContexts[i].origin
		a.searchContexts[i] = searchNavigation{}
		a.searchContexts = a.searchContexts[:i]
		a.restoreNavigation(&n)
		if a.view == ViewArticles && a.currentFeed != nil {
			if len(a.articles) == 0 {
				return a.loadArticles(a.currentFeed.ID)
			}
			if n.articlesLoadingMore {
				a.articlesLoadingMore = true
				return a.loadMoreArticles(a.currentFeed.ID, a.articlesCursor)
			}
		}
	}
	return a.resumeReader()
}

// Explicit article-context commands also work in independently built views.
func (a *App) contextualSearchTarget() *storage.Article {
	if len(a.searchContexts) == 0 {
		return a.currentArticle
	}
	return a.searchContextArticle()
}

func (a *App) searchContextArticle() *storage.Article {
	if len(a.searchContexts) > 0 {
		n := &a.searchContexts[len(a.searchContexts)-1].origin
		if n.view == ViewReader || (n.view == ViewMedia && n.mediaPreviousView == ViewReader) {
			return n.article
		}
		return nil
	}
	if a.previousView == ViewReader {
		return a.currentArticle
	}
	return nil
}
