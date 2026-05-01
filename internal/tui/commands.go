package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

func (a *App) loadFeeds() tea.Cmd {
	return func() tea.Msg {
		feeds, err := a.store.GetAllFeeds()
		if err != nil {
			return errorMsg{err: err}
		}
		return feedsLoadedMsg{feeds: feeds}
	}
}

func (a *App) loadArticles(feedID string) tea.Cmd {
	return func() tea.Msg {
		articles, err := a.store.GetArticles(feedID, DefaultArticleLimit)
		if err != nil {
			return errorMsg{err: wrapErr("load articles", err)}
		}
		return articlesLoadedMsg{articles: articles}
	}
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
		truncated := content[:maxSize-100] // Leave room for truncation message
		return truncated + "\n\n**[Content truncated due to size limit]**"
	}
	return content
}

func (a *App) renderArticle(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		var content strings.Builder

		// Apply size limits for security and performance
		safeTitle := sanitizeAndLimitContent(article.Title, maxTitleSize)
		content.WriteString(fmt.Sprintf("# %s\n\n", safeTitle))
		content.WriteString(fmt.Sprintf("*Published: %s*\n\n", article.Published.Format(time.RFC1123)))

		if article.URL != "" {
			safeURL := sanitizeAndLimitContent(article.URL, maxURLSize)
			content.WriteString(fmt.Sprintf("[Read Online](%s)\n\n", safeURL))
		}

		if len(article.MediaURLs) > 0 {
			content.WriteString("**Media:**\n")
			for _, url := range article.MediaURLs {
				safeMediaURL := sanitizeAndLimitContent(url, maxURLSize)
				content.WriteString(fmt.Sprintf("- %s\n", safeMediaURL))
			}
			content.WriteString("\n")
		}

		content.WriteString("---\n\n")

		// Apply content size limits with appropriate maximums
		if article.Content != "" {
			safeContent := sanitizeAndLimitContent(article.Content, maxContentSize)
			content.WriteString(safeContent)
		} else {
			safeDescription := sanitizeAndLimitContent(article.Description, maxDescriptionSize)
			content.WriteString(safeDescription)
		}

		// Use cached renderer for better performance
		r, err := a.getRenderer()
		if err != nil {
			return articleRenderedMsg{content: "Error initializing renderer: " + err.Error()}
		}

		rendered, err := r.Render(content.String())
		if err != nil {
			// Return articleRenderedMsg with error message for consistency
			// This ensures loadingArticle flag is always cleared
			return articleRenderedMsg{content: fmt.Sprintf("# Error\n\nFailed to render article: %s\n\nPress Escape to go back.", err.Error())}
		}

		if err := a.store.MarkArticleRead(article.ID, true); err != nil {
			_ = err // Explicitly ignore error
		}

		return articleRenderedMsg{content: rendered}
	}
}

func (a *App) addFeed(url string) tea.Cmd {
	return func() tea.Msg {
		url = strings.TrimSpace(url)
		if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
			url = "https://" + url
		}

		newFeed, err := a.manager.AddFeed(url)
		if err != nil {
			return feedAddedMsg{err: wrapErr("add feed", err)}
		}

		// Pull article count from the store; the manager has already
		// persisted them and notified search-index listeners.
		articles, _ := a.store.GetArticles(newFeed.ID, 0)
		return feedAddedMsg{err: nil, added: len(articles), title: newFeed.Title}
	}
}

func (a *App) renameFeed(newTitle string) tea.Cmd {
	return func() tea.Msg {
		if a.feedToRename == nil {
			return feedRenamedMsg{err: fmt.Errorf("no feed selected for rename")}
		}
		f := *a.feedToRename
		f.Title = strings.TrimSpace(newTitle)
		if f.Title == "" {
			return feedRenamedMsg{err: fmt.Errorf("title cannot be empty")}
		}
		f.UpdatedAt = time.Now()
		if err := a.store.SaveFeed(&f); err != nil {
			return feedRenamedMsg{err: err}
		}
		// Notify search engine about renamed feed so the index reflects the new title
		if ul, ok := a.searchEngine.(search.UpdateListener); ok {
			ul.OnDataUpdated(&f, nil)
		}
		return feedRenamedMsg{err: nil}
	}
}

func (a *App) refreshFeeds() tea.Cmd {
	return func() tea.Msg {
		summary, _ := a.manager.RefreshAllFeeds()

		docCount := -1
		if ds, ok := a.searchEngine.(search.DebugStatser); ok {
			if n, err := ds.DocCount(); err == nil {
				docCount = n
			}
		}

		return refreshDoneMsg{
			updatedFeeds:  summary.UpdatedFeeds,
			addedArticles: summary.AddedArticles,
			errors:        len(summary.Errors),
			docCount:      docCount,
		}
	}
}

func (a *App) toggleRead(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		if err := a.store.MarkArticleRead(article.ID, !article.Read); err != nil {
			return errorMsg{err: err}
		}
		return a.loadArticles(article.FeedID)()
	}
}

func (a *App) markArticleRead(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		if !article.Read {
			if err := a.store.MarkArticleRead(article.ID, true); err != nil {
				return errorMsg{err: err}
			}
			article.Read = true
		}
		return nil
	}
}

func (a *App) deleteFeed(feedID string) tea.Cmd {
	return func() tea.Msg {
		if err := a.store.DeleteFeed(feedID); err != nil {
			return feedDeletedMsg{err: wrapErr("delete feed", err)}
		}
		if dl, ok := a.searchEngine.(search.DeleteListener); ok {
			dl.OnFeedDeleted(feedID)
		}
		return feedDeletedMsg{err: nil}
	}
}

func (a *App) performSearch(query string) tea.Cmd {
	return a.performSearchWithContext(query, "")
}

func (a *App) performSearchWithContext(query, context string) tea.Cmd {
	return func() tea.Msg {
		// Use the new intelligent search engine
		var searchResults []*search.Result
		var err error

		if context == "article" && a.currentArticle != nil {
			// Search within current article
			searchResults, err = a.searchEngine.SearchInArticle(a.currentArticle, query)
			// If no results in-article, fall back to global to avoid empty UX
			if err == nil && len(searchResults) == 0 {
				searchResults, err = a.searchEngine.Search(query, 20)
			}
		} else {
			// Global search with limit
			searchResults, err = a.searchEngine.Search(query, 20)
		}

		if err != nil {
			return errorMsg{err: err}
		}

		// Convert search engine results to UI results
		var results []searchResultItem
		for _, sr := range searchResults {
			results = append(results, searchResultItem{
				feed:      sr.Feed,
				article:   sr.Article,
				isArticle: sr.IsArticle,
				icons:     a.icons,
			})
		}

		return searchResultsMsg{results: results}
	}
}


