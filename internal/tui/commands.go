package tui

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
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
		articles, err := a.store.GetArticles(feedID, 50)
		if err != nil {
			return errorMsg{err: err}
		}
		return articlesLoadedMsg{articles: articles}
	}
}

func (a *App) renderArticle(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		var content strings.Builder
		content.WriteString(fmt.Sprintf("# %s\n\n", article.Title))
		content.WriteString(fmt.Sprintf("*Published: %s*\n\n", article.Published.Format(time.RFC1123)))

		if article.URL != "" {
			content.WriteString(fmt.Sprintf("[Read Online](%s)\n\n", article.URL))
		}

		if len(article.MediaURLs) > 0 {
			content.WriteString("**Media:**\n")
			for _, url := range article.MediaURLs {
				content.WriteString(fmt.Sprintf("- %s\n", url))
			}
			content.WriteString("\n")
		}

		content.WriteString("---\n\n")

		if article.Content != "" {
			content.WriteString(article.Content)
		} else {
			content.WriteString(article.Description)
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

		feedID := fmt.Sprintf("%x", sha256.Sum256([]byte(url)))

		newFeed := &storage.Feed{
			ID:  feedID,
			URL: url,
		}

		resp, updated, err := a.fetcher.Fetch(newFeed)
		if err != nil {
			return feedAddedMsg{err: err}
		}

		if !updated || resp == nil {
			return feedAddedMsg{err: fmt.Errorf("feed not modified")}
		}

		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return feedAddedMsg{err: err}
		}

		articles, err := a.parser.Parse(strings.NewReader(string(body)), feedID)
		if err != nil {
			return feedAddedMsg{err: err}
		}

		if len(articles) > 0 && articles[0].Title != "" {
			newFeed.Title = extractFeedTitle(articles)
		}

		a.fetcher.UpdateFeedMetadata(newFeed, resp)

		if err := retryOperation(func() error { return a.store.SaveFeed(newFeed) }); err != nil {
			return feedAddedMsg{err: err}
		}

		if err := retryOperation(func() error { return a.store.SaveArticles(articles) }); err != nil {
			return feedAddedMsg{err: err}
		}

		return feedAddedMsg{err: nil}
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
		return feedRenamedMsg{err: nil}
	}
}

func (a *App) refreshFeeds() tea.Cmd {
	return func() tea.Msg {
		feeds, err := a.store.GetAllFeeds()
		if err != nil {
			return errorMsg{err: err}
		}

		for _, feed := range feeds {
			resp, updated, fetchErr := a.fetcher.Fetch(feed)
			if fetchErr != nil || !updated || resp == nil {
				continue
			}

			func() {
				defer resp.Body.Close()

				body, readErr := io.ReadAll(resp.Body)
				if readErr != nil {
					return
				}

				articles, parseErr := a.parser.Parse(strings.NewReader(string(body)), feed.ID)
				if parseErr != nil {
					return
				}

				a.fetcher.UpdateFeedMetadata(feed, resp)
				if saveErr := retryOperation(func() error { return a.store.SaveFeed(feed) }); saveErr != nil {
					return
				}
				if saveErr := retryOperation(func() error { return a.store.SaveArticles(articles) }); saveErr != nil {
					return
				}
			}()
		}

		return a.loadFeeds()()
	}
}

func (a *App) toggleRead(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		err := retryOperation(func() error { return a.store.MarkArticleRead(article.ID, !article.Read) })
		if err != nil {
			return errorMsg{err: err}
		}
		return a.loadArticles(article.FeedID)()
	}
}

func (a *App) markArticleRead(article *storage.Article) tea.Cmd {
	return func() tea.Msg {
		if !article.Read {
			err := retryOperation(func() error { return a.store.MarkArticleRead(article.ID, true) })
			if err != nil {
				return errorMsg{err: err}
			}
			// Mark local copy as read too
			article.Read = true
		}
		// Return nil message to not trigger any view updates
		return nil
	}
}

func (a *App) deleteFeed(feedID string) tea.Cmd {
	return func() tea.Msg {
		err := retryOperation(func() error { return a.store.DeleteFeed(feedID) })
		return feedDeletedMsg{err: err}
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
			})
		}

		return searchResultsMsg{results: results}
	}
}

// retryOperation retries a database operation up to 3 times with exponential backoff
func retryOperation(operation func() error) error {
	maxRetries := 3
	baseDelay := 100 * time.Millisecond

	var lastErr error
	for i := 0; i < maxRetries; i++ {
		if err := operation(); err != nil {
			lastErr = err
			if i < maxRetries-1 {
				delay := baseDelay * time.Duration(1<<i) // exponential backoff
				time.Sleep(delay)
				continue
			}
		} else {
			return nil
		}
	}
	return lastErr
}

func extractFeedTitle(articles []*storage.Article) string {
	if len(articles) > 0 {
		parts := strings.SplitN(articles[0].URL, "/", 4)
		if len(parts) >= 3 {
			return parts[2]
		}
	}
	return "Unknown Feed"
}
