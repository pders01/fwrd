package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
)

// DefaultOpenTimeout is the bolt file-lock acquisition timeout used
// when callers do not provide their own.
const DefaultOpenTimeout = 1 * time.Second

var (
	feedsBucket                = []byte("feeds")
	articlesBucket             = []byte("articles")
	metaBucket                 = []byte("metadata")
	articlesByFeedBucket       = []byte("articles_by_feed")
	articlesByDateBucket       = []byte("articles_by_date")
	articlesUnreadByFeedBucket = []byte("articles_unread_by_feed")
)

// unreadIndexFlag marks (in metaBucket) that the unread index has been
// back-filled for a pre-existing database.
var unreadIndexFlag = []byte("unread_index_v1")

// boltBackend is the local bbolt implementation of storeBackend.
type boltBackend struct {
	db       *bolt.DB
	tempPath string // non-empty when the backend owns a temp file (MemoryPath)
}

// newBoltBackend opens the database at dbPath, creates the standard
// buckets, and back-fills the unread index if needed.
func newBoltBackend(dbPath string, timeout time.Duration) (*boltBackend, error) {
	tempPath := ""
	if dbPath == MemoryPath {
		f, err := os.CreateTemp("", "fwrd-mem-*.db")
		if err != nil {
			return nil, fmt.Errorf("creating temp database: %w", err)
		}
		tempPath = f.Name()
		_ = f.Close()
		dbPath = tempPath
	}

	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: timeout})
	if err != nil {
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		if errors.Is(err, bolterrors.ErrTimeout) {
			return nil, fmt.Errorf("%w (waited %s for %s)", ErrDatabaseLocked, timeout, dbPath)
		}
		return nil, fmt.Errorf("opening database: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{feedsBucket, articlesBucket, metaBucket, articlesByFeedBucket, articlesByDateBucket, articlesUnreadByFeedBucket} {
			if _, createErr := tx.CreateBucketIfNotExists(bucket); createErr != nil {
				return createErr
			}
		}
		return buildUnreadIndexIfNeeded(tx)
	})

	if err != nil {
		_ = db.Close()
		if tempPath != "" {
			_ = os.Remove(tempPath)
		}
		return nil, fmt.Errorf("creating buckets: %w", err)
	}

	return &boltBackend{db: db, tempPath: tempPath}, nil
}

func (b *boltBackend) Close() error {
	closeErr := b.db.Close()
	if b.tempPath != "" {
		if rmErr := os.Remove(b.tempPath); rmErr != nil && !os.IsNotExist(rmErr) && closeErr == nil {
			closeErr = rmErr
		}
	}
	return closeErr
}

func (b *boltBackend) SaveFeed(feed *Feed) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(feedsBucket)
		data, err := json.Marshal(feed)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(feed.ID), data)
	})
}

func (b *boltBackend) GetFeed(id string) (*Feed, error) {
	var feed Feed
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(feedsBucket)
		data := bucket.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("feed not found")
		}
		return json.Unmarshal(data, &feed)
	})
	return &feed, err
}

func (b *boltBackend) GetAllFeeds() ([]*Feed, error) {
	var feeds []*Feed
	err := b.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(feedsBucket)
		return bucket.ForEach(func(_ []byte, v []byte) error {
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

func (b *boltBackend) FeedStats() (map[string]FeedStat, error) {
	stats := map[string]FeedStat{}
	err := b.db.View(func(tx *bolt.Tx) error {
		if idxRoot := tx.Bucket(articlesByFeedBucket); idxRoot != nil {
			if err := idxRoot.ForEach(func(feedID, _ []byte) error {
				if fb := idxRoot.Bucket(feedID); fb != nil {
					st := stats[string(feedID)]
					st.Total = fb.Stats().KeyN
					stats[string(feedID)] = st
				}
				return nil
			}); err != nil {
				return err
			}
		}
		if unreadRoot := tx.Bucket(articlesUnreadByFeedBucket); unreadRoot != nil {
			return unreadRoot.ForEach(func(feedID, _ []byte) error {
				if fb := unreadRoot.Bucket(feedID); fb != nil {
					st := stats[string(feedID)]
					st.Unread = fb.Stats().KeyN
					stats[string(feedID)] = st
				}
				return nil
			})
		}
		return nil
	})
	return stats, err
}

func (b *boltBackend) SaveArticles(articles []*Article) error {
	_, err := b.saveArticles(articles, false)
	return err
}

func (b *boltBackend) MergeArticles(articles []*Article) ([]*Article, error) {
	return b.saveArticles(articles, true)
}

func (b *boltBackend) saveArticles(articles []*Article, preserveState bool) ([]*Article, error) {
	saved := make([]*Article, 0, len(articles))
	err := b.db.Update(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		idxRoot := tx.Bucket(articlesByFeedBucket)
		dateIdx := tx.Bucket(articlesByDateBucket)
		for _, incoming := range articles {
			if incoming == nil {
				return fmt.Errorf("article must not be nil")
			}
			// Work on a copy: a failed transaction must not change caller state.
			article := *incoming
			var prevPublished time.Time
			hadPrev := false
			if existing := ab.Get([]byte(article.ID)); existing != nil {
				var old Article
				if err := json.Unmarshal(existing, &old); err != nil {
					return fmt.Errorf("decode existing article %s: %w", article.ID, err)
				}
				prevPublished, hadPrev = old.Published, true
				if preserveState {
					article.Read, article.Starred = old.Read, old.Starred
				}
			}
			data, err := json.Marshal(&article)
			if err != nil {
				return err
			}
			if err := ab.Put([]byte(article.ID), data); err != nil {
				return err
			}

			if idxRoot != nil {
				fb, err := idxRoot.CreateBucketIfNotExists([]byte(article.FeedID))
				if err != nil {
					return err
				}
				if err := fb.Put([]byte(article.ID), []byte{1}); err != nil {
					return err
				}
			}

			if err := setUnreadMembership(tx, article.FeedID, article.ID, !article.Read); err != nil {
				return err
			}

			if dateIdx != nil {
				if hadPrev && !prevPublished.Equal(article.Published) {
					_ = dateIdx.Delete(makeDateIndexKey(prevPublished, article.ID))
				}
				dateKey := makeDateIndexKey(article.Published, article.ID)
				if err := dateIdx.Put(dateKey, []byte(article.ID)); err != nil {
					return err
				}
			}
			saved = append(saved, &article)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (b *boltBackend) GetArticle(id string) (*Article, error) {
	var article Article
	err := b.db.View(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		if ab == nil {
			return fmt.Errorf("article not found")
		}
		data := ab.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("article not found")
		}
		return json.Unmarshal(data, &article)
	})
	if err != nil {
		return nil, err
	}
	return &article, nil
}

func (b *boltBackend) GetArticles(feedID string, limit int) ([]*Article, error) {
	return b.GetArticlesWithCursor(feedID, limit, "")
}

func (b *boltBackend) GetArticlesWithCursor(feedID string, limit int, cursor string) ([]*Article, error) {
	var articles []*Article

	err := b.db.View(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		if ab == nil {
			return nil
		}

		if feedID != "" {
			return b.getArticlesForFeed(tx, ab, feedID, limit, cursor, &articles)
		}
		return b.getArticlesGlobalOptimized(tx, ab, limit, cursor, &articles)
	})

	return articles, err
}

// getArticlesForFeed collects all articles in feedID's per-feed bucket,
// sorts by Published descending, then applies cursor + limit.
func (b *boltBackend) getArticlesForFeed(tx *bolt.Tx, ab *bolt.Bucket, feedID string, limit int, cursor string, articles *[]*Article) error {
	idxRoot := tx.Bucket(articlesByFeedBucket)
	if idxRoot == nil {
		return nil
	}

	fb := idxRoot.Bucket([]byte(feedID))
	if fb == nil {
		return nil
	}

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

	sort.SliceStable(*articles, func(i, j int) bool {
		ai, aj := (*articles)[i], (*articles)[j]
		if !ai.Published.Equal(aj.Published) {
			return ai.Published.After(aj.Published)
		}
		return ai.ID < aj.ID
	})

	if cursor != "" {
		cursorIndex := -1
		for i, article := range *articles {
			if article.ID == cursor {
				cursorIndex = i
				break
			}
		}
		if cursorIndex >= 0 && cursorIndex+1 < len(*articles) {
			*articles = (*articles)[cursorIndex+1:]
		} else {
			*articles = []*Article{}
		}
	}

	if limit > 0 && len(*articles) > limit {
		*articles = (*articles)[:limit]
	}

	return nil
}

// getArticlesGlobalOptimized retrieves articles across all feeds using the date index.
func (b *boltBackend) getArticlesGlobalOptimized(tx *bolt.Tx, ab *bolt.Bucket, limit int, cursor string, articles *[]*Article) error {
	dateIdx := tx.Bucket(articlesByDateBucket)
	if dateIdx == nil {
		return ab.ForEach(func(_ []byte, v []byte) error {
			var article Article
			if err := json.Unmarshal(v, &article); err != nil {
				return nil
			}
			*articles = append(*articles, &article)
			return nil
		})
	}

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

// mutateArticle loads the article by id, applies fn, and writes it back.
// The unread index is updated if Read changed.
func (b *boltBackend) mutateArticle(id string, fn func(*Article)) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		data := ab.Get([]byte(id))
		if data == nil {
			return fmt.Errorf("article not found")
		}

		var article Article
		if err := json.Unmarshal(data, &article); err != nil {
			return err
		}

		wasRead := article.Read
		fn(&article)

		data, err := json.Marshal(article)
		if err != nil {
			return err
		}

		if article.Read != wasRead {
			if err := setUnreadMembership(tx, article.FeedID, article.ID, !article.Read); err != nil {
				return err
			}
		}

		return ab.Put([]byte(id), data)
	})
}

func (b *boltBackend) MarkArticleRead(id string, read bool) error {
	return b.mutateArticle(id, func(a *Article) { a.Read = read })
}

func (b *boltBackend) MarkArticleStarred(id string, starred bool) error {
	return b.mutateArticle(id, func(a *Article) { a.Starred = starred })
}

func (b *boltBackend) DeleteFeed(id string) error {
	return b.db.Update(func(tx *bolt.Tx) error {
		feedBucket := tx.Bucket(feedsBucket)
		if err := feedBucket.Delete([]byte(id)); err != nil {
			return err
		}

		if unreadRoot := tx.Bucket(articlesUnreadByFeedBucket); unreadRoot != nil {
			if unreadRoot.Bucket([]byte(id)) != nil {
				if err := unreadRoot.DeleteBucket([]byte(id)); err != nil {
					return fmt.Errorf("deleting per-feed unread index bucket: %w", err)
				}
			}
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

		c := fb.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			articleID := append([]byte(nil), k...)
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

		if err := idxRoot.DeleteBucket([]byte(id)); err != nil {
			return fmt.Errorf("deleting per-feed index bucket: %w", err)
		}
		return nil
	})
}

// ---- helpers ----

func makeDateIndexKey(published time.Time, articleID string) []byte {
	timestamp := published.UnixNano()
	reverseTimestamp := ^timestamp

	key := make([]byte, 8+len(articleID))
	binary.BigEndian.PutUint64(key[:8], uint64(reverseTimestamp))
	copy(key[8:], articleID)
	return key
}

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
	if bytes.Equal(key, want) {
		key, articleID = dateCursor.Next()
	}
	return key, articleID
}

func setUnreadMembership(tx *bolt.Tx, feedID, articleID string, unread bool) error {
	unreadRoot := tx.Bucket(articlesUnreadByFeedBucket)
	if unreadRoot == nil {
		return nil
	}
	if unread {
		fb, err := unreadRoot.CreateBucketIfNotExists([]byte(feedID))
		if err != nil {
			return err
		}
		return fb.Put([]byte(articleID), []byte{1})
	}
	if fb := unreadRoot.Bucket([]byte(feedID)); fb != nil {
		return fb.Delete([]byte(articleID))
	}
	return nil
}

func buildUnreadIndexIfNeeded(tx *bolt.Tx) error {
	meta := tx.Bucket(metaBucket)
	if meta != nil && meta.Get(unreadIndexFlag) != nil {
		return nil
	}
	ab := tx.Bucket(articlesBucket)
	unreadRoot := tx.Bucket(articlesUnreadByFeedBucket)
	if ab != nil && unreadRoot != nil {
		err := ab.ForEach(func(_, v []byte) error {
			var a Article
			if json.Unmarshal(v, &a) != nil || a.Read {
				return nil
			}
			fb, err := unreadRoot.CreateBucketIfNotExists([]byte(a.FeedID))
			if err != nil {
				return err
			}
			return fb.Put([]byte(a.ID), []byte{1})
		})
		if err != nil {
			return err
		}
	}
	if meta != nil {
		return meta.Put(unreadIndexFlag, []byte{1})
	}
	return nil
}
