package storage

import (
	"errors"
	"fmt"

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
		if ipc.ErrorCode(err) == ipc.CodeLocked {
			return fmt.Errorf("%s: %w", method, errors.Join(ErrDatabaseLocked, err))
		}
		return fmt.Errorf("%s: %w", method, err)
	}
	return nil
}

func (r *rpcBackend) SaveFeed(feed *Feed) error {
	return r.call("SaveFeed", feed, nil)
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
	const pageSize = 1000
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
