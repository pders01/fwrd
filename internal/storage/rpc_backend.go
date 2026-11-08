package storage

import (
	"errors"
	"fmt"
	"time"

	"github.com/pders01/fwrd/internal/ipc"
)

// rpcBackend implements storeBackend by forwarding each method to the
// serve process over IPC. It is used by CLI/TUI processes when a serve
// instance already owns the database.
type rpcBackend struct {
	client *ipc.Client
}

// newRPCBackend wraps an already-connected IPC client.
func newRPCBackend(client *ipc.Client) *rpcBackend {
	return &rpcBackend{client: client}
}

func (r *rpcBackend) call(method string, params, result any) error {
	if err := r.client.Call(method, params, result); err != nil {
		switch ipc.ErrorCode(err) {
		case ipc.CodeLocked:
			return fmt.Errorf("%s: %w", method, errors.Join(ErrDatabaseLocked, err))
		case ipc.CodeFeedExists:
			return fmt.Errorf("%s: %w", method, errors.Join(ErrFeedExists, err))
		case ipc.CodeArticleFeedChanged:
			return fmt.Errorf("%s: %w", method, errors.Join(ErrArticleFeedChanged, err))
		case ipc.CodeResultTooLarge:
			return fmt.Errorf("%s: %w", method, errors.Join(ErrResultTooLarge, err))
		}
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (r *rpcBackend) SaveFeed(feed *Feed) error {
	return r.call("SaveFeed", feed, nil)
}

func (r *rpcBackend) CommitFeed(feed *Feed, articles []*Article, createOnly bool) ([]*Article, error) {
	var saved []*Article
	err := r.call("CommitFeed", map[string]any{"feed": feed, "articles": articles, "create_only": createOnly}, &saved)
	return saved, err
}

func (r *rpcBackend) CommitFeedCounted(feed *Feed, articles []*Article, createOnly bool) (FeedCommitResult, error) {
	var result FeedCommitResult
	if err := r.call("CommitFeedCounted", map[string]any{"feed": feed, "articles": articles, "create_only": createOnly}, &result); err != nil {
		return FeedCommitResult{}, err
	}
	return result, nil
}

func (r *rpcBackend) SetFeedError(id, message string, at time.Time) (*Feed, error) {
	var feed Feed
	if err := r.call("SetFeedError", map[string]any{"id": id, "message": message, "at": at}, &feed); err != nil {
		return nil, err
	}
	return &feed, nil
}

func (r *rpcBackend) SetFeedDesk(id, deskID string) (*Feed, error) {
	var feed Feed
	if err := r.call("SetFeedDesk", map[string]string{"id": id, "desk_id": deskID}, &feed); err != nil {
		return nil, err
	}
	return &feed, nil
}

func (r *rpcBackend) RenameFeed(id, title string) (*Feed, error) {
	var feed Feed
	if err := r.call("RenameFeed", map[string]string{"id": id, "title": title}, &feed); err != nil {
		return nil, err
	}
	return &feed, nil
}

func (r *rpcBackend) GetFeed(id string) (*Feed, error) {
	var feed Feed
	if err := r.call("GetFeed", map[string]string{"id": id}, &feed); err != nil {
		return nil, err
	}
	return &feed, nil
}

func (r *rpcBackend) GetAllFeeds() ([]*Feed, error) {
	var feeds []*Feed
	if err := r.call("GetAllFeeds", nil, &feeds); err != nil {
		return nil, err
	}
	return feeds, nil
}

func (r *rpcBackend) SaveArticles(articles []*Article) error {
	return r.call("SaveArticles", articles, nil)
}

func (r *rpcBackend) MergeArticles(articles []*Article) ([]*Article, error) {
	var saved []*Article
	if err := r.call("MergeArticles", articles, &saved); err != nil {
		return nil, err
	}
	return saved, nil
}

func (r *rpcBackend) GetArticle(id string) (*Article, error) {
	var article Article
	if err := r.call("GetArticle", map[string]string{"id": id}, &article); err != nil {
		return nil, err
	}
	return &article, nil
}

func (r *rpcBackend) GetArticles(feedID string, limit int) ([]*Article, error) {
	return r.GetArticlesWithCursor(feedID, limit, "")
}

func (r *rpcBackend) GetArticlesWithCursor(feedID string, limit int, cursor string) ([]*Article, error) {
	pageSize := 1000
	articles := make([]*Article, 0)
	for limit <= 0 || len(articles) < limit {
		pageLimit := pageSize
		if limit > 0 {
			pageLimit = min(pageLimit, limit-len(articles))
		}
		params := map[string]any{
			"feed_id": feedID,
			"limit":   pageLimit,
			"cursor":  cursor,
		}
		var page []*Article
		if err := r.call("GetArticlesWithCursor", params, &page); err != nil {
			if errors.Is(err, ipc.ErrResultTooLarge) {
				if pageLimit == 1 {
					return nil, fmt.Errorf("single article after cursor %q exceeds IPC response limit: %w", cursor, err)
				}
				pageSize = max(1, pageLimit/2)
				continue
			}
			return nil, err
		}
		articles = append(articles, page...)
		if len(page) < pageLimit {
			break
		}
		nextCursor := page[len(page)-1].ID
		if nextCursor == "" || nextCursor == cursor {
			return nil, errors.New("GetArticlesWithCursor: server returned an invalid pagination cursor")
		}
		cursor = nextCursor
	}
	return articles, nil
}

func (r *rpcBackend) GetArticlesWithCursorBounded(feedID string, limit int, cursor string, maxBytes int) ([]*Article, error) {
	var articles []*Article
	err := r.call("GetArticlesWithCursor", map[string]any{
		"feed_id": feedID, "limit": limit, "cursor": cursor, "max_bytes": maxBytes,
	}, &articles)
	return articles, err
}

func (r *rpcBackend) FeedStats() (map[string]FeedStat, error) {
	var stats map[string]FeedStat
	if err := r.call("FeedStats", nil, &stats); err != nil {
		return nil, err
	}
	return stats, nil
}

func (r *rpcBackend) MarkArticleRead(id string, read bool) error {
	return r.call("MarkArticleRead", map[string]any{"id": id, "read": read}, nil)
}

func (r *rpcBackend) MarkArticleStarred(id string, starred bool) error {
	return r.call("MarkArticleStarred", map[string]any{"id": id, "starred": starred}, nil)
}

func (r *rpcBackend) DeleteFeed(id string) error {
	return r.call("DeleteFeed", map[string]string{"id": id}, nil)
}

func (r *rpcBackend) Close() error {
	return r.client.Close()
}
