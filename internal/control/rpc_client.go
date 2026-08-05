package control

import (
	"context"
	"fmt"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

const (
	operationSlack      = 30 * time.Second
	maxOperationTimeout = 24 * time.Hour
)

// remoteOps implements FeedOperations by forwarding each call to the serve
// process. Feed operation deadlines are derived from configuration rather than
// the short default used for storage reads.
type remoteOps struct {
	client        *ipc.Client
	feedTimeout   time.Duration
	maxConcurrent int
}

func newRemoteOps(client *ipc.Client, cfg *config.Config) *remoteOps {
	timeout := 30 * time.Second
	workers := config.DefaultMaxConcurrentRefreshes
	if cfg != nil {
		if cfg.Feed.HTTPTimeout > 0 {
			timeout = cfg.Feed.HTTPTimeout
		}
		if cfg.Feed.MaxConcurrentRefreshes > 0 {
			workers = cfg.Feed.MaxConcurrentRefreshes
		}
	}
	return &remoteOps{client: client, feedTimeout: timeout, maxConcurrent: workers}
}

func (r *remoteOps) AddFeed(ctx context.Context, url string) (*storage.Feed, error) {
	var value storage.Feed
	// Plugin enhancement and the feed request can each consume the configured
	// HTTP timeout.
	timeout := boundedOperationTimeout(2*r.feedTimeout + operationSlack)
	if err := r.client.CallWithContext(ctx, "AddFeed", map[string]string{"url": url}, &value, timeout); err != nil {
		return nil, fmt.Errorf("AddFeed: %w", err)
	}
	return &value, nil
}

func (r *remoteOps) RefreshFeed(ctx context.Context, id string, force bool) error {
	timeout := boundedOperationTimeout(r.feedTimeout + operationSlack)
	if err := r.client.CallWithContext(ctx, "RefreshFeed", map[string]any{"id": id, "force": force}, nil, timeout); err != nil {
		return fmt.Errorf("RefreshFeed: %w", err)
	}
	return nil
}

func (r *remoteOps) RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error) {
	// Count feeds first so the deadline scales with the number of worker waves.
	// One extra wave covers a feed added between this read and coordinator lock.
	var feeds []*storage.Feed
	if err := r.client.CallWithContext(ctx, "GetAllFeeds", nil, &feeds, 30*time.Second); err != nil {
		return feed.RefreshSummary{}, fmt.Errorf("RefreshAllFeeds preflight: %w", err)
	}
	workers := max(r.maxConcurrent, 1)
	waves := (len(feeds)+workers-1)/workers + 1
	timeout := boundedOperationTimeout(time.Duration(waves)*r.feedTimeout + operationSlack)

	var summary feed.RefreshSummary
	if err := r.client.CallWithContext(ctx, "RefreshAllFeeds", map[string]bool{"force": force}, &summary, timeout); err != nil {
		return summary, fmt.Errorf("RefreshAllFeeds: %w", err)
	}
	return summary, nil
}

func (r *remoteOps) DeleteFeed(ctx context.Context, id string) error {
	// Large Bleve indexes can take longer than a storage read to purge.
	timeout := boundedOperationTimeout(max(5*time.Minute, r.feedTimeout+operationSlack))
	if err := r.client.CallWithContext(ctx, "DeleteFeed", map[string]string{"id": id}, nil, timeout); err != nil {
		return fmt.Errorf("DeleteFeed: %w", err)
	}
	return nil
}

func (r *remoteOps) ImportFeeds(ctx context.Context, urls []string) (ImportSummary, error) {
	// Imports are sequential and each URL can run plugin enhancement plus a
	// feed request.
	timeout := boundedOperationTimeout(time.Duration(max(len(urls), 1))*2*r.feedTimeout + operationSlack)
	var summary ImportSummary
	if err := r.client.CallWithContext(ctx, "ImportFeeds", map[string][]string{"urls": urls}, &summary, timeout); err != nil {
		return summary, fmt.Errorf("ImportFeeds: %w", err)
	}
	return summary, nil
}

func boundedOperationTimeout(timeout time.Duration) time.Duration {
	if timeout <= 0 {
		return operationSlack
	}
	if timeout > maxOperationTimeout {
		return maxOperationTimeout
	}
	return timeout
}
