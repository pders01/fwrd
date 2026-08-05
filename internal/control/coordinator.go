package control

import (
	"context"
	"errors"
	"sync"

	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

// MutationCoordinator serializes all mutating operations — whether from the
// web server or IPC — so Bleve listener notifications cannot race. Its
// channel-based lock lets queued operations be cancelled during shutdown.
type MutationCoordinator struct {
	ops FeedOperations

	token      chan struct{}
	closing    chan struct{}
	closeOnce  sync.Once
	operations context.Context
	cancel     context.CancelFunc
}

// NewMutationCoordinator wraps a FeedOperations implementation with a shared,
// cancellation-aware mutation lock.
func NewMutationCoordinator(ops FeedOperations) *MutationCoordinator {
	operations, cancel := context.WithCancel(context.Background())
	c := &MutationCoordinator{
		ops:        ops,
		token:      make(chan struct{}, 1),
		closing:    make(chan struct{}),
		operations: operations,
		cancel:     cancel,
	}
	c.token <- struct{}{}
	return c
}

// BeginShutdown rejects operations that have not acquired the mutation lock.
// The operation currently holding it may continue through the grace period.
func (c *MutationCoordinator) BeginShutdown() {
	c.closeOnce.Do(func() { close(c.closing) })
}

// CancelOperations cancels the operation currently running. Feed fetches and
// queued manager work propagate this cancellation to their HTTP requests.
func (c *MutationCoordinator) CancelOperations() { c.cancel() }

func (c *MutationCoordinator) operationContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.closing:
		return nil, nil, context.Canceled
	default:
	}
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case <-c.closing:
		return nil, nil, context.Canceled
	case <-c.token:
	}
	// If shutdown raced with lock acquisition, return the token and reject the
	// operation rather than starting new work after shutdown began.
	select {
	case <-c.closing:
		c.token <- struct{}{}
		return nil, nil, context.Canceled
	default:
	}

	opCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(c.operations, cancel)
	return opCtx, func() {
		stop()
		cancel()
		c.token <- struct{}{}
	}, nil
}

func (c *MutationCoordinator) AddFeed(ctx context.Context, url string) (*storage.Feed, error) {
	opCtx, done, err := c.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer done()
	return c.ops.AddFeed(opCtx, url)
}

func (c *MutationCoordinator) RefreshFeed(ctx context.Context, id string, force bool) error {
	opCtx, done, err := c.operationContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return c.ops.RefreshFeed(opCtx, id, force)
}

func (c *MutationCoordinator) RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error) {
	opCtx, done, err := c.operationContext(ctx)
	if err != nil {
		return feed.RefreshSummary{}, err
	}
	defer done()
	return c.ops.RefreshAllFeeds(opCtx, force)
}

func (c *MutationCoordinator) DeleteFeed(ctx context.Context, id string) error {
	opCtx, done, err := c.operationContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	return c.ops.DeleteFeed(opCtx, id)
}

// SaveFeed serializes a primitive feed write with its search notification.
func (c *MutationCoordinator) SaveFeed(ctx context.Context, value *storage.Feed) error {
	_, done, err := c.operationContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	ops, ok := c.ops.(interface{ SaveFeed(*storage.Feed) error })
	if !ok {
		return errors.New("primitive SaveFeed is unavailable")
	}
	return ops.SaveFeed(value)
}

// SaveArticles serializes primitive article writes with search notification.
func (c *MutationCoordinator) SaveArticles(ctx context.Context, articles []*storage.Article) error {
	_, done, err := c.operationContext(ctx)
	if err != nil {
		return err
	}
	defer done()
	ops, ok := c.ops.(interface {
		SaveArticles([]*storage.Article) error
	})
	if !ok {
		return errors.New("primitive SaveArticles is unavailable")
	}
	return ops.SaveArticles(articles)
}

func (c *MutationCoordinator) ImportFeeds(ctx context.Context, urls []string) (ImportSummary, error) {
	opCtx, done, err := c.operationContext(ctx)
	if err != nil {
		return ImportSummary{}, err
	}
	defer done()
	return c.ops.ImportFeeds(opCtx, urls)
}
