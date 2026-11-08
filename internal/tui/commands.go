package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/terminaltext"
)

func (a *App) loadFeeds() tea.Cmd {
	return a.loadFeedMetadata(false)
}

// Reconciliation must not replace the mutation error (or a newer unrelated
// error) even if the authoritative metadata read itself fails.
func (a *App) reconcileFeeds() tea.Cmd {
	return a.loadFeedMetadata(true)
}

func (a *App) loadFeedMetadata(preserveError bool) tea.Cmd {
	a.feedsSeq++
	seq, store := a.feedsSeq, a.store
	return a.worker(func() tea.Msg {
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return errorMsg{err: err, feedsSeq: seq, preserveError: preserveError}
		}
		return feedsLoadedMsg{feeds: feeds, seq: seq}
	})
}

func (a *App) loadArticles(feedID string) tea.Cmd {
	// Never show the previous feed's articles while the new request is pending.
	a.articles = nil
	a.articleList.SetItems(nil)
	a.articlesCursor = ""
	a.articlesHasMore = false
	a.articlesLoadingMore = false
	return a.loadArticlesPage(feedID, "", false)
}

// loadMoreArticles fetches the next page of articles for feedID,
// continuing after the supplied cursor. Used by the article list to
// stream additional pages when the user scrolls toward the end.
func (a *App) loadMoreArticles(feedID, cursor string) tea.Cmd {
	return a.loadArticlesPage(feedID, cursor, true)
}

// loadArticlesPage is the shared implementation behind first-page
// loads and incremental "load more" calls. It returns one
// articlesLoadedMsg whose fields tell the Update handler whether to
// replace or append, and where the next cursor sits.
func (a *App) loadArticlesPage(feedID, cursor string, appendPage bool) tea.Cmd {
	a.articlesSeq++
	seq := a.articlesSeq
	limit := pickPositive(a.config.UI.Article.ListLimit, DefaultArticleLimit)
	store := a.store
	return a.worker(func() tea.Msg {
		articles, err := store.GetArticlesWithCursor(feedID, limit, cursor)
		if err != nil {
			return articlesLoadedMsg{feedID: feedID, seq: seq, appendPage: appendPage, err: wrapErr("load articles", err)}
		}
		nextCursor := ""
		hasMore := false
		if limit > 0 && len(articles) == limit {
			hasMore = true
			nextCursor = articles[len(articles)-1].ID
		}
		return articlesLoadedMsg{
			feedID:     feedID,
			seq:        seq,
			articles:   articles,
			appendPage: appendPage,
			cursor:     nextCursor,
			hasMore:    hasMore,
		}
	})
}

// articleListPrefetchMargin is how many items from the bottom of the
// article list will trigger a prefetch of the next page. A small margin
// keeps memory bounded; a non-zero value avoids the user noticing the
// fetch latency at the very last item.
const articleListPrefetchMargin = 10

// maybeLoadMoreArticles dispatches the next pagination request when the
// user has scrolled near the end of the loaded article list. Returns
// nil when no fetch is needed (no current feed, no remaining pages, an
// in-flight request, or the selection is far from the end).
func (a *App) maybeLoadMoreArticles() tea.Cmd {
	if !a.articlesHasMore || a.articlesLoadingMore || a.currentFeed == nil || a.articlesCursor == "" {
		return nil
	}
	items := a.articleList.Items()
	if len(items) == 0 {
		return nil
	}
	if a.articleList.Index() < len(items)-articleListPrefetchMargin {
		return nil
	}
	a.articlesLoadingMore = true
	return a.loadMoreArticles(a.currentFeed.ID, a.articlesCursor)
}

// Content size limits for security and performance
const (
	maxContentSize     = 1024 * 1024 // 1MB max content size
	maxDescriptionSize = 64 * 1024   // 64KB max description size
	maxTitleSize       = 1024        // 1KB max title size
	maxURLSize         = 2048        // 2KB max URL size
)

// sanitizeAndLimitContent safely truncates content to prevent memory issues
// and protects against malicious oversized content.
func sanitizeAndLimitContent(content string, maxSize int) string {
	if len(content) > maxSize {
		debuglog.Warnf("Content size (%d bytes) exceeds limit (%d bytes), truncating", len(content), maxSize)
		truncated := strings.ToValidUTF8(content[:max(0, maxSize-100)], "") // Leave room for truncation message
		// Safe discards any control sequence cut short at the size limit.
		return terminaltext.Safe(truncated) + "\n\n**[Content truncated due to size limit]**"
	}
	return terminaltext.Safe(content)
}

func (a *App) renderArticle(article *storage.Article) tea.Cmd {
	// Resolve the renderer on the calling goroutine (Bubble Tea's
	// main goroutine, since renderArticle runs from Update). The
	// returned closure runs in a tea.Cmd goroutine and must not touch
	// App fields concurrently with Update — capturing r and rerr by
	// value avoids a race against tea.WindowSizeMsg handling.
	r, rerr := a.getRenderer()
	if a.loadingArticle {
		a.readerInitialPending = true
	}
	a.renderSeq++
	seq := a.renderSeq
	article = snapshotArticle(article)
	id := article.ID
	renderMu := &a.renderMu
	return a.worker(func() tea.Msg {
		var content strings.Builder

		// Apply size limits for security and performance
		safeTitle := sanitizeAndLimitContent(article.Title, maxTitleSize)
		fmt.Fprintf(&content, "# %s\n\n", safeTitle)
		fmt.Fprintf(&content, "*Published: %s*\n\n", article.Published.Format(time.RFC1123))

		if article.URL != "" {
			safeURL := sanitizeAndLimitContent(article.URL, maxURLSize)
			fmt.Fprintf(&content, "[Read Online](%s)\n\n", safeURL)
		}

		if len(article.MediaURLs) > 0 {
			content.WriteString("**Media:**\n")
			for _, url := range article.MediaURLs {
				safeMediaURL := sanitizeAndLimitContent(url, maxURLSize)
				fmt.Fprintf(&content, "- %s\n", safeMediaURL)
			}
			content.WriteString("\n")
		}

		content.WriteString("---\n\n")

		// Apply content size limits with appropriate maximums
		if article.Content != "" {
			safeContent := sanitizeAndLimitContent(article.Content, maxContentSize)
			content.WriteString(htmlToMarkdown(safeContent))
		} else {
			safeDescription := sanitizeAndLimitContent(article.Description, maxDescriptionSize)
			content.WriteString(htmlToMarkdown(safeDescription))
		}

		if rerr != nil {
			return articleRenderedMsg{articleID: id, seq: seq, err: wrapErr("initialize renderer", rerr)}
		}

		// Cached Glamour renderers hold mutable buffers. Every Cmd sharing
		// one must own it exclusively for the entire Render call.
		renderMu.Lock()
		rendered, err := r.Render(safeMarkdownInput(content.String()))
		renderMu.Unlock()
		if err != nil {
			// Return articleRenderedMsg with error message for consistency
			// This ensures loadingArticle flag is always cleared
			return articleRenderedMsg{articleID: id, seq: seq, err: wrapErr("render article", err)}
		}

		// Read-state side-effect lives in markArticleRead, which is
		// dispatched alongside this command from the article-open path.
		// Duplicating the write here was a relic from before that split.

		return articleRenderedMsg{articleID: id, seq: seq, content: rendered}
	})
}

func (a *App) addFeed(url string) tea.Cmd {
	ops, store, ctx := a.feedOps, a.store, a.commandContext()
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	return a.worker(func() tea.Msg {
		if ops == nil {
			return feedAddedMsg{err: fmt.Errorf("feed operations are unavailable"), notAttempted: true}
		}
		newFeed, err := ops.AddFeed(ctx, url)
		if err != nil {
			return feedAddedMsg{err: wrapErr("add feed", err)}
		}

		// Pull article count from the store; the active local/remote operation
		// has already persisted them and notified search-index listeners.
		articles, _ := store.GetArticles(newFeed.ID, 0)
		return feedAddedMsg{err: nil, added: len(articles), title: newFeed.Title}
	})
}

func (a *App) renameFeed(newTitle string) tea.Cmd {
	id := ""
	if a.feedToRename != nil {
		id = a.feedToRename.ID
	}
	store, engine, ops, ctx := a.store, a.searchEngine, a.feedOps, a.commandContext()
	newTitle = strings.TrimSpace(newTitle)
	return a.worker(func() tea.Msg {
		if id == "" {
			return feedRenamedMsg{err: fmt.Errorf("no feed selected for rename"), notAttempted: true}
		}
		if newTitle == "" {
			return feedRenamedMsg{err: fmt.Errorf("title cannot be empty"), notAttempted: true}
		}
		// Local construction uses the coordinator so rename + notification
		// cannot interleave with a refresh batch. Remote stores route this
		// same atomic operation to the server's coordinator through RPC.
		if renamer, ok := ops.(interface {
			RenameFeed(context.Context, string, string) (*storage.Feed, error)
		}); ok {
			_, err := renamer.RenameFeed(ctx, id, newTitle)
			return feedRenamedMsg{err: err}
		}
		f, err := store.RenameFeed(id, newTitle)
		if err != nil {
			return feedRenamedMsg{err: err}
		}
		// Local engines need the committed record. Remote Store.RenameFeed
		// notifies serve's index via RPC; its TUI fallback has no listener.
		if ul, ok := engine.(search.UpdateListener); ok {
			ul.OnDataUpdated(f, nil)
		}
		return feedRenamedMsg{}
	})
}

func (a *App) refreshFeeds() tea.Cmd {
	a.refreshSeq++
	// Older metadata reads must not overwrite this refresh's eventual state
	// or surface stale errors while its replacement read is still pending.
	a.feedsSeq++
	seq := a.refreshSeq
	ops, engine, force, ctx := a.feedOps, a.searchEngine, a.forceRefresh, a.commandContext()
	return a.worker(func() tea.Msg {
		if ops == nil {
			return errorMsg{err: fmt.Errorf("feed operations are unavailable"), refreshSeq: seq}
		}
		summary, err := ops.RefreshAllFeeds(ctx, force)
		// A feed's own timeout is a partial result, not cancellation of the
		// session. Check the actual command context, not errors.Is(err, ...).
		if ctxErr := ctx.Err(); ctxErr != nil {
			return errorMsg{err: wrapErr("refresh feeds", errors.Join(err, ctxErr)), refreshSeq: seq}
		}
		if err != nil && !summary.OnlyFeedErrors(err) {
			return errorMsg{err: wrapErr("refresh feeds", err), refreshSeq: seq}
		}

		docCount := -1
		if ds, ok := engine.(search.DebugStatser); ok {
			if n, err := ds.DocCount(); err == nil {
				docCount = n
			}
		}

		return refreshDoneMsg{
			seq:           seq,
			updatedFeeds:  summary.UpdatedFeeds,
			addedArticles: summary.AddedArticles,
			errors:        len(summary.Errors),
			docCount:      docCount,
		}
	})
}

func (a *App) toggleRead(article *storage.Article) tea.Cmd {
	return a.changeArticleState(article, false, true)
}

func (a *App) toggleStarred(article *storage.Article) tea.Cmd {
	return a.changeArticleState(article, true, true)
}

func (a *App) markArticleRead(article *storage.Article) tea.Cmd {
	return a.changeArticleState(article, false, false)
}

func (a *App) deleteFeed(feedID string) tea.Cmd {
	ops, ctx := a.feedOps, a.commandContext()
	return a.worker(func() tea.Msg {
		if ops == nil {
			return feedDeletedMsg{err: fmt.Errorf("feed operations are unavailable"), notAttempted: true}
		}
		if err := ops.DeleteFeed(ctx, feedID); err != nil {
			return feedDeletedMsg{err: wrapErr("delete feed", err)}
		}
		return feedDeletedMsg{err: nil}
	})
}

func (a *App) performSearch(query string) tea.Cmd {
	return a.performSearchWithContext(query, "")
}

func (a *App) performSearchWithContext(query, searchContext string) tea.Cmd {
	a.searchSeq++
	seq, engine, icons := a.searchSeq, a.searchEngine, a.icons
	var article *storage.Article
	id := ""
	if searchContext == "article" {
		article = snapshotArticle(a.contextualSearchTarget())
		if article != nil {
			id = article.ID
		}
	}
	return a.worker(func() tea.Msg {
		// Use the new intelligent search engine
		var searchResults []*search.Result
		var err error

		if article != nil {
			// Search a detached snapshot, never a live model pointer.
			searchResults, err = engine.SearchInArticle(article, query)
			// If no results in-article, fall back to global to avoid empty UX
			if err == nil && len(searchResults) == 0 {
				searchResults, err = engine.Search(query, defaultSearchResultLimit)
			}
		} else {
			searchResults, err = engine.Search(query, defaultSearchResultLimit)
		}

		if err != nil {
			return searchResultsMsg{seq: seq, articleID: id, err: err}
		}

		// Convert search engine results to UI results
		var results []searchResultItem
		for _, sr := range searchResults {
			results = append(results, searchResultItem{
				feed:      sr.Feed,
				article:   sr.Article,
				isArticle: sr.IsArticle,
				icons:     &icons,
			})
		}

		return searchResultsMsg{seq: seq, articleID: id, results: results}
	})
}
