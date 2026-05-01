package feed

import "github.com/pders01/fwrd/internal/storage"

// DataListener receives notifications after Manager persists feed data.
// Implementations must not block — notification is synchronous from the
// goroutine that drove the underlying operation. RefreshAllFeeds collects
// per-feed results and dispatches notifications from a single goroutine,
// so listener implementations do not need to be safe for concurrent
// notification.
type DataListener interface {
	OnDataUpdated(feed *storage.Feed, articles []*storage.Article)
}

// BatchScope brackets a multi-feed operation so listeners that batch work
// (e.g. a search index using grouped writes) can amortise overhead across
// many feeds. RefreshAllFeeds calls BeginBatch before any notifications
// and CommitBatch after the last one.
type BatchScope interface {
	BeginBatch()
	CommitBatch()
}

// RefreshSummary reports the outcome of RefreshAllFeeds.
type RefreshSummary struct {
	UpdatedFeeds  int
	AddedArticles int
	Errors        []error
}
