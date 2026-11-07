// Package control provides the shared FeedOperations interface and the
// session abstraction that CLI commands use to access storage and feed
// lifecycle operations, whether locally or over IPC.
package control

import (
	"context"

	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

// FeedOperations groups feed lifecycle methods that must run in the
// database-owning process so that plugins, Bleve notifications, and
// audit logging are all driven by the serve-side manager.
type FeedOperations interface {
	AddFeed(ctx context.Context, url string) (*storage.Feed, error)
	RefreshFeed(ctx context.Context, id string, force bool) error
	RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error)
	DeleteFeed(ctx context.Context, id string) error
	ImportFeeds(ctx context.Context, urls []string) (ImportSummary, error)
}

// ImportSummary reports the outcome of ImportFeeds.
type ImportSummary struct {
	Added    int
	Skipped  int
	Failed   int
	Failures []ImportFailure `json:"failures,omitempty"`
}

// ImportFailure identifies a URL that could not be added.
type ImportFailure struct {
	URL   string `json:"url"`
	Error string `json:"error"`
}

// LocalOps is the local (non-IPC) FeedOperations implementation that
// delegates directly to feed.Manager and storage.Store.
type LocalOps struct {
	Store   *storage.Store
	Manager *feed.Manager
}

func (l *LocalOps) AddFeed(ctx context.Context, url string) (*storage.Feed, error) {
	return l.Manager.AddFeedContext(ctx, url)
}

func (l *LocalOps) RefreshFeed(ctx context.Context, id string, force bool) error {
	previous := l.Manager.ForceRefresh()
	l.Manager.SetForceRefresh(force)
	defer l.Manager.SetForceRefresh(previous)
	return l.Manager.RefreshFeedContext(ctx, id)
}

func (l *LocalOps) RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error) {
	previous := l.Manager.ForceRefresh()
	l.Manager.SetForceRefresh(force)
	defer l.Manager.SetForceRefresh(previous)
	return l.Manager.RefreshAllFeedsContext(ctx)
}

func (l *LocalOps) DeleteFeed(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return l.Manager.DeleteFeed(id)
}

// SaveFeed performs a primitive storage mutation and emits the same search
// notification as a manager-driven operation.
func (l *LocalOps) SaveFeed(value *storage.Feed) error {
	if err := l.Store.SaveFeed(value); err != nil {
		return err
	}
	l.Manager.NotifyDataUpdated(value, nil)
	return nil
}

// SaveArticles performs a primitive storage mutation and updates search.
func (l *LocalOps) SaveArticles(articles []*storage.Article) error {
	if err := l.Store.SaveArticles(articles); err != nil {
		return err
	}
	l.Manager.NotifyDataUpdated(nil, articles)
	return nil
}

// MergeArticles refreshes article content and indexes the committed records.
func (l *LocalOps) MergeArticles(articles []*storage.Article) ([]*storage.Article, error) {
	saved, err := l.Store.MergeArticles(articles)
	if err != nil {
		return nil, err
	}
	l.Manager.NotifyDataUpdated(nil, saved)
	return saved, nil
}

// CommitFeed publishes only committed feed/article state to search listeners.
func (l *LocalOps) CommitFeed(value *storage.Feed, articles []*storage.Article, createOnly bool) ([]*storage.Article, error) {
	saved, err := l.Store.CommitFeed(value, articles, createOnly)
	if err != nil {
		return nil, err
	}
	committed, err := l.Store.GetFeed(value.ID)
	if err != nil {
		return nil, err
	}
	l.Manager.NotifyDataUpdated(committed, saved)
	return saved, nil
}

// SetFeedDesk atomically changes a feed's desk and updates search.
func (l *LocalOps) SetFeedDesk(id, deskID string) (*storage.Feed, error) {
	value, err := l.Store.SetFeedDesk(id, deskID)
	if err != nil {
		return nil, err
	}
	l.Manager.NotifyDataUpdated(value, nil)
	return value, nil
}

// RenameFeed atomically changes a feed's title override and updates search.
func (l *LocalOps) RenameFeed(id, title string) (*storage.Feed, error) {
	value, err := l.Store.RenameFeed(id, title)
	if err != nil {
		return nil, err
	}
	l.Manager.NotifyDataUpdated(value, nil)
	return value, nil
}

func (l *LocalOps) ImportFeeds(ctx context.Context, urls []string) (ImportSummary, error) {
	if err := ctx.Err(); err != nil {
		return ImportSummary{}, err
	}
	// Snapshot existing URLs so already-subscribed feeds are skipped.
	existing, err := l.Store.GetAllFeeds()
	if err != nil {
		return ImportSummary{}, err
	}
	have := make(map[string]bool, 2*len(existing))
	for _, f := range existing {
		have[f.URL] = true
		have[f.SubscriptionURL()] = true
	}

	var summary ImportSummary
	for _, u := range urls {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		if have[u] {
			summary.Skipped++
			continue
		}
		if _, addErr := l.Manager.AddFeedContext(ctx, u); addErr != nil {
			if ctx.Err() != nil {
				return summary, ctx.Err()
			}
			summary.Failed++
			summary.Failures = append(summary.Failures, ImportFailure{URL: u, Error: addErr.Error()})
			continue
		}
		have[u] = true
		summary.Added++
	}
	return summary, nil
}
