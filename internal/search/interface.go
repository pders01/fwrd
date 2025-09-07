package search

import "github.com/pders01/fwrd/internal/storage"

// Searcher defines the minimal search API used by the TUI.
type Searcher interface {
	Search(query string, limit int) ([]*Result, error)
	SearchInArticle(article *storage.Article, query string) ([]*Result, error)
}

// UpdateListener can be implemented by search engines that maintain
// an external index and want to be notified about data changes.
type UpdateListener interface {
	OnDataUpdated(feed *storage.Feed, articles []*storage.Article)
}

// DeleteListener can be implemented to get notified when a feed is deleted.
type DeleteListener interface {
	OnFeedDeleted(feedID string)
}

// DebugStatser provides lightweight stats for visibility/debugging.
// Implemented by engines that can report index doc counts, etc.
type DebugStatser interface {
	DocCount() (int, error)
}
