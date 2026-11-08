package feed

import (
	"errors"

	"github.com/pders01/fwrd/internal/storage"
)

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

// DeleteListener receives notification after a feed is deleted.
type DeleteListener interface {
	OnFeedDeleted(feedID string)
}

// RefreshSummary reports the outcome of RefreshAllFeeds.
type RefreshSummary struct {
	UpdatedFeeds  int
	AddedArticles int
	Errors        []error
}

// OnlyFeedErrors reports whether every cause of err is represented in Errors.
// Callers must also check their operation context: a feed's HTTP timeout can
// share a cancellation sentinel with a top-level canceled refresh. Error text
// alone cannot distinguish unrelated failures with identical messages.
func (s RefreshSummary) OnlyFeedErrors(err error) bool {
	if err == nil {
		return false
	}
	for _, feedErr := range s.Errors {
		if errors.Is(feedErr, err) {
			return true
		}
	}
	switch wrapped := err.(type) {
	case interface{ Unwrap() []error }:
		causes := wrapped.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !s.OnlyFeedErrors(cause) {
				return false
			}
		}
		return true
	case interface{ Unwrap() error }:
		return s.OnlyFeedErrors(wrapped.Unwrap())
	default:
		return false
	}
}
