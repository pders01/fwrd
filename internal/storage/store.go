package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// MemoryPath is the sentinel database path that requests an isolated,
// process-local store backed by a unique temp file. bbolt has no real
// in-memory mode, so the store creates the file in os.TempDir() and
// deletes it on Close.
const MemoryPath = ":memory:"

var (
	feedsBucket    = []byte("feeds")
	articlesBucket = []byte("articles")
	metaBucket     = []byte("metadata")
	// Index bucket: articles_by_feed -> sub-bucket per feedID containing article IDs
	articlesByFeedBucket = []byte("articles_by_feed")
	// Index bucket: articles_by_date -> stores article IDs keyed by date for efficient date-sorted queries
	articlesByDateBucket = []byte("articles_by_date")
)

type Store struct {
	db       *bolt.DB
	tempPath string // non-empty when the store owns a temp file (MemoryPath)
}

// makeDateIndexKey creates a key for the date index that ensures newest-first ordering
// when iterated with a cursor. Uses reverse timestamp (max - timestamp) for descending order.
func makeDateIndexKey(published time.Time, articleID string) []byte {
	// Use nanoseconds since epoch, inverted for reverse order
	timestamp := published.UnixNano()
	reverseTimestamp := ^timestamp // Bitwise NOT for reverse ordering

	key := make([]byte, 8+len(articleID))
	binary.BigEndian.PutUint64(key[:8], uint64(reverseTimestamp))
	copy(key[8:], articleID) // Append article ID for uniqueness
	return key
}

// seekDateCursor positions a date-index cursor just past the entry identified
// by the given article ID. The cursor is encoded as the article ID so callers
// can pass back any article from a previous page; we look up the article's
// Published timestamp once to reconstruct the composite index key, then use
// bbolt's B+tree Seek (O(log n)) instead of a linear scan from First().
//
// Returns (nil, nil) when the cursor article has been deleted or when Seek
// runs off the end. The deletion case is treated as "no more results" so a
// concurrent DeleteFeed cannot silently restart pagination at page 1 for
// callers that hold a stale cursor.
//
// Pass cursor == "" to start from the first entry.
func seekDateCursor(ab *bolt.Bucket, dateCursor *bolt.Cursor, cursor string) (key, articleID []byte) {
	if cursor == "" {
		return dateCursor.First()
	}
	raw := ab.Get([]byte(cursor))
	if raw == nil {
		return nil, nil
	}
	var art Article
	if err := json.Unmarshal(raw, &art); err != nil {
		return nil, nil
	}
	want := makeDateIndexKey(art.Published, art.ID)
	key, articleID = dateCursor.Seek(want)
	if key == nil {
		return nil, nil
	}
	// Seek lands on the cursor entry itself when present; advance past it.
	if bytes.Equal(key, want) {
		key, articleID = dateCursor.Next()
	}
	return key, articleID
}

// DefaultOpenTimeout is the bolt file-lock acquisition timeout used
// when callers do not provide their own (NewStore).
const DefaultOpenTimeout = 1 * time.Second

func NewStore(dbPath string) (*Store, error) {
	return NewStoreWithTimeout(dbPath, DefaultOpenTimeout)
}

func NewStoreWithTimeout(dbPath string, timeout time.Duration) (*Store, error) {
	tempPath := ""
	if dbPath == MemoryPath {
		// bbolt has no in-memory mode; route the sentinel to a unique
		// temp file so callers passing MemoryPath always get an
		// isolated store. Without this, every call would open a
		// literal file named ":memory:" in the working directory and
		// share state across the process.
		f, err := os.CreateTemp("", "fwrd-mem-*.db")
		if err != nil {
			return nil, fmt.Errorf("creating temp database: %w", err)
		}
		tempPath = f.Name()
		// bbolt opens the file itself; close our handle so it can.
		_ = f.Close()
		dbPath = tempPath
	}

	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: timeout})
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, fmt.Errorf("opening database: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{feedsBucket, articlesBucket, metaBucket, articlesByFeedBucket, articlesByDateBucket} {
			if _, createErr := tx.CreateBucketIfNotExists(bucket); createErr != nil {
				return createErr
			}
		}
		return nil
	})

	if err != nil {
		_ = db.Close()
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, fmt.Errorf("creating buckets: %w", err)
	}

	return &Store{db: db, tempPath: tempPath}, nil
}

func (s *Store) Close() error {
	closeErr := s.db.Close()
	if s.tempPath != "" {
		if rmErr := os.Remove(s.tempPath); rmErr != nil && !os.IsNotExist(rmErr) && closeErr == nil {
			closeErr = rmErr
		}
	}
	return closeErr
}

func (s *Store) SaveFeed(feed *Feed) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(feedsBucket)
		data, err := json.Marshal(feed)
		if err != nil {
			return err
		}
		return b.Put([]byte(feed.ID), data)
	})
}

func (s *Store) GetFeed(id string) (*Feed, error) {
	var feed Feed
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(feedsBucket)
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("feed not found")
		}
		return json.Unmarshal(data, &feed)
	})
	return &feed, err
}

func (s *Store) GetAllFeeds() ([]*Feed, error) {
	if s == nil || s.db == nil {
		return []*Feed{}, nil
	}
	var feeds []*Feed
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(feedsBucket)
		return b.ForEach(func(_ []byte, v []byte) error {
			var feed Feed
			if err := json.Unmarshal(v, &feed); err != nil {
				return err
			}
			feeds = append(feeds, &feed)
			return nil
		})
	})
	// Sort feeds by Title (case-insensitive), fallback to URL
	sort.Slice(feeds, func(i, j int) bool {
		ti := feeds[i].Title
		tj := feeds[j].Title
		if ti == "" {
			ti = feeds[i].URL
		}
		if tj == "" {
			tj = feeds[j].URL
		}
		return strings.ToLower(ti) < strings.ToLower(tj)
	})
	return feeds, err
}

func (s *Store) SaveArticles(articles []*Article) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(articlesBucket)
		idxRoot := tx.Bucket(articlesByFeedBucket)
		dateIdx := tx.Bucket(articlesByDateBucket)
		for _, article := range articles {
			data, err := json.Marshal(article)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(article.ID), data); err != nil {
				return err
			}

			// Update feed index: ensure sub-bucket for this feed exists and record article ID
			if idxRoot != nil {
				fb, err := idxRoot.CreateBucketIfNotExists([]byte(article.FeedID))
				if err != nil {
					return err
				}
				if err := fb.Put([]byte(article.ID), []byte{1}); err != nil {
					return err
				}
			}

			// Update date index: store article ID with reverse timestamp key for newest-first ordering
			if dateIdx != nil {
				dateKey := makeDateIndexKey(article.Published, article.ID)
				if err := dateIdx.Put(dateKey, []byte(article.ID)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Store) GetArticles(feedID string, limit int) ([]*Article, error) {
	return s.GetArticlesWithCursor(feedID, limit, "")
}

// GetArticlesWithCursor provides cursor-based pagination for efficient large dataset traversal.
// cursor should be the article ID of the last article from the previous page, or empty for the first page.
func (s *Store) GetArticlesWithCursor(feedID string, limit int, cursor string) ([]*Article, error) {
	if s == nil || s.db == nil {
		return []*Article{}, nil
	}
	var articles []*Article

	err := s.db.View(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		if ab == nil {
			return nil
		}

		if feedID != "" {
			// Feed-scoped query: iterate the feed's own bucket. The
			// global date index would force us to scan every article
			// in the database to filter for one feed; the per-feed
			// bucket is bounded by len(articles_in_feed) and is
			// strictly cheaper for typical feed sizes.
			return s.getArticlesForFeed(tx, ab, feedID, limit, cursor, &articles)
		}

		// No feed specified: use date index for efficient sorted retrieval
		return s.getArticlesGlobalOptimized(tx, ab, limit, cursor, &articles)
	})

	return articles, err
}

// getArticlesForFeed collects all articles in feedID's per-feed bucket,
// sorts them by Published descending, then applies cursor + limit. The
// scan is O(len(feed)) regardless of how many other feeds exist.
func (s *Store) getArticlesForFeed(tx *bolt.Tx, ab *bolt.Bucket, feedID string, limit int, cursor string, articles *[]*Article) error {
	idxRoot := tx.Bucket(articlesByFeedBucket)
	if idxRoot == nil {
		return nil
	}

	fb := idxRoot.Bucket([]byte(feedID))
	if fb == nil {
		return nil
	}

	// Collect articles for this feed
	c := fb.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		v := ab.Get(k)
		if v == nil {
			continue
		}

		var article Article
		if err := json.Unmarshal(v, &article); err != nil {
			continue
		}

		*articles = append(*articles, &article)
	}

	// Sort by date (newest first)
	sort.Slice(*articles, func(i, j int) bool {
		return (*articles)[i].Published.After((*articles)[j].Published)
	})

	// Apply cursor-based filtering after sorting
	if cursor != "" {
		// Find cursor position and slice from there
		cursorIndex := -1
		for i, article := range *articles {
			if article.ID == cursor {
				cursorIndex = i
				break
			}
		}
		if cursorIndex >= 0 && cursorIndex+1 < len(*articles) {
			*articles = (*articles)[cursorIndex+1:] // Start after cursor
		} else {
			*articles = []*Article{} // Cursor not found or no more articles
		}
	}

	// Apply limit after cursor filtering
	if limit > 0 && len(*articles) > limit {
		*articles = (*articles)[:limit]
	}

	return nil
}

// getArticlesGlobalOptimized efficiently retrieves articles across all feeds using date index
func (s *Store) getArticlesGlobalOptimized(tx *bolt.Tx, ab *bolt.Bucket, limit int, cursor string, articles *[]*Article) error {
	dateIdx := tx.Bucket(articlesByDateBucket)
	if dateIdx == nil {
		// Fallback to scanning all articles
		return ab.ForEach(func(_ []byte, v []byte) error {
			var article Article
			if err := json.Unmarshal(v, &article); err != nil {
				return nil // Skip invalid articles
			}
			*articles = append(*articles, &article)
			return nil
		})
	}

	// Use date index cursor to iterate in date order (newest first)
	c := dateIdx.Cursor()
	count := 0

	k, articleID := seekDateCursor(ab, c, cursor)

	for ; k != nil && (limit <= 0 || count < limit); k, articleID = c.Next() {
		v := ab.Get(articleID)
		if v == nil {
			continue
		}

		var article Article
		if err := json.Unmarshal(v, &article); err != nil {
			continue
		}

		*articles = append(*articles, &article)
		count++
	}

	return nil
}

func (s *Store) MarkArticleRead(id string, read bool) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(articlesBucket)
		data := b.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("article not found")
		}

		var article Article
		if err := json.Unmarshal(data, &article); err != nil {
			return err
		}

		article.Read = read

		data, err := json.Marshal(article)
		if err != nil {
			return err
		}

		return b.Put([]byte(id), data)
	})
}

func (s *Store) DeleteFeed(id string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		feedBucket := tx.Bucket(feedsBucket)
		if err := feedBucket.Delete([]byte(id)); err != nil {
			return err
		}

		ab := tx.Bucket(articlesBucket)
		dateIdx := tx.Bucket(articlesByDateBucket)
		idxRoot := tx.Bucket(articlesByFeedBucket)
		if idxRoot == nil {
			return nil
		}

		fb := idxRoot.Bucket([]byte(id))
		if fb == nil {
			return nil
		}

		// Walk the per-feed sub-bucket once; for each article ID, look
		// up its full record before deleting so we can reconstruct the
		// composite date-index key and remove that entry by Seek/Delete
		// instead of a linear scan.
		c := fb.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			articleID := append([]byte(nil), k...) // Cursor keys are tx-scoped; copy.
			if ab != nil && dateIdx != nil {
				if data := ab.Get(articleID); data != nil {
					var art Article
					if err := json.Unmarshal(data, &art); err == nil {
						dateKey := makeDateIndexKey(art.Published, art.ID)
						if err := dateIdx.Delete(dateKey); err != nil {
							return fmt.Errorf("deleting date-index entry: %w", err)
						}
					}
				}
			}
			if ab != nil {
				if err := ab.Delete(articleID); err != nil {
					return fmt.Errorf("deleting article %s: %w", articleID, err)
				}
			}
		}

		// Drop the per-feed sub-bucket. Propagating the error here is
		// load-bearing: the surrounding tx will roll back every prior
		// delete, so the post-failure state is the original feed +
		// articles + indexes, not a half-deleted carcass.
		if err := idxRoot.DeleteBucket([]byte(id)); err != nil {
			return fmt.Errorf("deleting per-feed index bucket: %w", err)
		}
		return nil
	})
}
