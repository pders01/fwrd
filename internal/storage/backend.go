package storage

import "errors"

// ErrDatabaseLocked is returned when bbolt cannot acquire the file lock
// within the configured timeout — almost always because another fwrd
// process already has the database open.
var ErrDatabaseLocked = errors.New("database is locked by another fwrd process")

// MemoryPath is the sentinel database path that requests an isolated,
// process-local store backed by a unique temp file. bbolt has no real
// in-memory mode, so the store creates the file in os.TempDir() and
// deletes it on Close.
const MemoryPath = ":memory:"

// storeBackend abstracts the underlying data-access mechanism.
// Implementations include the local bbolt store (boltBackend) and
// the IPC-backed client (rpcBackend).
type storeBackend interface {
	SaveFeed(feed *Feed) error
	GetFeed(id string) (*Feed, error)
	GetAllFeeds() ([]*Feed, error)
	SaveArticles(articles []*Article) error
	MergeArticles(articles []*Article) ([]*Article, error)
	GetArticle(id string) (*Article, error)
	GetArticles(feedID string, limit int) ([]*Article, error)
	GetArticlesWithCursor(feedID string, limit int, cursor string) ([]*Article, error)
	FeedStats() (map[string]FeedStat, error)
	MarkArticleRead(id string, read bool) error
	MarkArticleStarred(id string, starred bool) error
	DeleteFeed(id string) error
	Close() error
}
