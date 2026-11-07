package storage

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/pders01/fwrd/internal/ipc"
)

// Store is the data-access facade used throughout the application. It
// delegates to a storeBackend implementation and tracks a monotonically
// increasing write generation so callers (e.g. the web front-page cache)
// can detect stale derived state without coordinating with writers.
type Store struct {
	backend  storeBackend
	writeGen atomic.Uint64
}

// WriteGen returns a counter that strictly increases on every store
// mutation. A caller can cache derived state and rebuild only when the
// value changes. A nil store reports 0.
func (s *Store) WriteGen() uint64 {
	if s == nil {
		return 0
	}
	return s.writeGen.Load()
}

// NewStore opens a local bbolt-backed store at dbPath with the default
// file-lock timeout.
func NewStore(dbPath string) (*Store, error) {
	return NewLocalStoreWithTimeout(dbPath, DefaultOpenTimeout)
}

// NewStoreWithTimeout opens a local bbolt-backed store with a custom
// file-lock timeout.
func NewStoreWithTimeout(dbPath string, timeout time.Duration) (*Store, error) {
	return NewLocalStoreWithTimeout(dbPath, timeout)
}

// NewLocalStoreWithTimeout opens a local bbolt-backed store with a custom
// file-lock timeout. This is the explicit local-only constructor; serve
// must use this (never an IPC fallback) to guarantee it owns the database.
func NewLocalStoreWithTimeout(dbPath string, timeout time.Duration) (*Store, error) {
	be, err := newBoltBackend(dbPath, timeout)
	if err != nil {
		return nil, err
	}
	return &Store{backend: be}, nil
}

// NewRPCStore constructs a Store backed by an already-connected IPC client.
// The backend remains private so callers cannot depend on implementation
// details of the Store facade.
func NewRPCStore(client *ipc.Client) *Store {
	return &Store{backend: newRPCBackend(client)}
}

// Close releases underlying resources. For a local store this closes the
// database and removes any temp file (MemoryPath).
func (s *Store) Close() error {
	if s == nil || s.backend == nil {
		return nil
	}
	return s.backend.Close()
}

// SaveFeed persists a feed. On success writeGen is incremented.
func (s *Store) SaveFeed(feed *Feed) error {
	err := s.backend.SaveFeed(feed)
	if err == nil {
		s.writeGen.Add(1)
	}
	return err
}

// CommitFeed atomically saves feed metadata and merges articles, preserving
// stored reader state and (on refresh) user-owned feed fields. Failed commits
// leave both caller values and the database unchanged.
func (s *Store) CommitFeed(feed *Feed, articles []*Article, createOnly bool) ([]*Article, error) {
	saved, err := s.backend.CommitFeed(feed, articles, createOnly)
	if err == nil {
		s.writeGen.Add(1)
	}
	return saved, err
}

// SetFeedDesk changes only the desk of an existing feed.
func (s *Store) SetFeedDesk(id, deskID string) (*Feed, error) {
	feed, err := s.backend.SetFeedDesk(id, deskID)
	if err == nil {
		s.writeGen.Add(1)
	}
	return feed, err
}

// RenameFeed sets a persistent title override on an existing feed.
func (s *Store) RenameFeed(id, title string) (*Feed, error) {
	feed, err := s.backend.RenameFeed(id, title)
	if err == nil {
		s.writeGen.Add(1)
	}
	return feed, err
}

// GetFeed retrieves a single feed by ID.
func (s *Store) GetFeed(id string) (*Feed, error) {
	return s.backend.GetFeed(id)
}

// GetAllFeeds returns every stored feed, sorted by title (case-insensitive).
// Returns an empty slice when the store is nil or the database is unset.
func (s *Store) GetAllFeeds() ([]*Feed, error) {
	if s == nil || s.backend == nil {
		return []*Feed{}, nil
	}
	return s.backend.GetAllFeeds()
}

// FeedStats returns per-feed unread and total article counts from indexed
// sub-buckets. Feeds with no articles are absent from the map.
func (s *Store) FeedStats() (map[string]FeedStat, error) {
	stats := map[string]FeedStat{}
	if s == nil || s.backend == nil {
		return stats, nil
	}
	return s.backend.FeedStats()
}

// SaveArticles persists articles. On success writeGen is incremented.
func (s *Store) SaveArticles(articles []*Article) error {
	err := s.backend.SaveArticles(articles)
	if err == nil {
		s.writeGen.Add(1)
	}
	return err
}

// MergeArticles refreshes content while atomically preserving the current
// Read/Starred flags of existing articles. New articles keep their supplied
// flags (including onboarding state). It returns the records actually saved.
func (s *Store) MergeArticles(articles []*Article) ([]*Article, error) {
	saved, err := s.backend.MergeArticles(articles)
	if err == nil {
		s.writeGen.Add(1)
	}
	return saved, err
}

// GetArticle fetches a single article by ID.
func (s *Store) GetArticle(id string) (*Article, error) {
	if s == nil || s.backend == nil {
		return nil, fmt.Errorf("store not initialized")
	}
	return s.backend.GetArticle(id)
}

// GetArticles returns articles for a feed (or all if feedID is ""),
// limited to limit results (0 means unlimited).
func (s *Store) GetArticles(feedID string, limit int) ([]*Article, error) {
	if s == nil || s.backend == nil {
		return []*Article{}, nil
	}
	return s.backend.GetArticles(feedID, limit)
}

// GetArticlesWithCursor provides cursor-based pagination. cursor should
// be the article ID of the last article from the previous page, or empty
// for the first page.
func (s *Store) GetArticlesWithCursor(feedID string, limit int, cursor string) ([]*Article, error) {
	if s == nil || s.backend == nil {
		return []*Article{}, nil
	}
	return s.backend.GetArticlesWithCursor(feedID, limit, cursor)
}

// GetArticlesWithCursorBounded is a page read with an additional serialized
// payload budget. It fails before decoding records exceeding that budget;
// RPC uses it to bound response memory independently of article count.
func (s *Store) GetArticlesWithCursorBounded(feedID string, limit int, cursor string, maxBytes int) ([]*Article, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("maxBytes must be positive")
	}
	return s.backend.GetArticlesWithCursorBounded(feedID, limit, cursor, maxBytes)
}

// MarkArticleRead sets the Read flag on an article. On success writeGen
// is incremented.
func (s *Store) MarkArticleRead(id string, read bool) error {
	err := s.backend.MarkArticleRead(id, read)
	if err == nil {
		s.writeGen.Add(1)
	}
	return err
}

// MarkArticleStarred flips an article's Starred flag. On success writeGen
// is incremented.
func (s *Store) MarkArticleStarred(id string, starred bool) error {
	err := s.backend.MarkArticleStarred(id, starred)
	if err == nil {
		s.writeGen.Add(1)
	}
	return err
}

// DeleteFeed removes a feed and all its articles and index entries. On
// success writeGen is incremented.
func (s *Store) DeleteFeed(id string) error {
	err := s.backend.DeleteFeed(id)
	if err == nil {
		s.writeGen.Add(1)
	}
	return err
}
