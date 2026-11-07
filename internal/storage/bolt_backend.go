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
	articlesByFeedDateBucket   = []byte("articles_by_feed_date")
	articlesUnreadByFeedBucket = []byte("articles_unread_by_feed")
)

// unreadIndexFlag marks (in metaBucket) that the unread index has been
// back-filled for a pre-existing database.
var unreadIndexFlag = []byte("unread_index_v1")

// Version 2 uses signed seconds plus nanoseconds and per-feed date indexes.
// Derived indexes are rebuilt in one transaction, including after interruption.
var dateIndexFlag = []byte("date_index_v2")

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
		if rebuildErr := rebuildDateIndexesIfNeeded(tx); rebuildErr != nil {
			return rebuildErr
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

func (b *boltBackend) CommitFeed(feed *Feed, articles []*Article, createOnly bool) ([]*Article, error) {
	if feed == nil {
		return nil, fmt.Errorf("feed must not be nil")
	}
	var saved []*Article
	err := b.db.Update(func(tx *bolt.Tx) error {
		committed := *feed
		fb := tx.Bucket(feedsBucket)
		if raw := fb.Get([]byte(feed.ID)); raw != nil {
			if createOnly {
				return ErrFeedExists
			}
			var current Feed
			if err := json.Unmarshal(raw, &current); err != nil {
				return err
			}
			committed.DeskID = current.DeskID
			committed.SubscribedURL = current.SubscribedURL
			committed.SubscribedTitle = current.SubscribedTitle
			committed.SubscribedAt = current.SubscribedAt
			committed.TitleOverride = current.TitleOverride
			if current.TitleOverride != "" {
				committed.Title = current.TitleOverride
			}
		} else if !createOnly {
			return fmt.Errorf("feed not found")
		}
		for _, article := range articles {
			if article == nil || article.FeedID != feed.ID {
				return fmt.Errorf("article must belong to committed feed")
			}
		}
		data, err := json.Marshal(&committed)
		if err != nil {
			return err
		}
		if putErr := fb.Put([]byte(feed.ID), data); putErr != nil {
			return putErr
		}
		saved, err = saveArticlesTx(tx, articles, true)
		return err
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func (b *boltBackend) mutateFeed(id string, mutate func(*Feed)) (*Feed, error) {
	var feed Feed
	err := b.db.Update(func(tx *bolt.Tx) error {
		fb := tx.Bucket(feedsBucket)
		raw := fb.Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("feed not found")
		}
		if err := json.Unmarshal(raw, &feed); err != nil {
			return err
		}
		mutate(&feed)
		data, err := json.Marshal(&feed)
		if err != nil {
			return err
		}
		return fb.Put([]byte(id), data)
	})
	if err != nil {
		return nil, err
	}
	return &feed, nil
}

func (b *boltBackend) SetFeedDesk(id, deskID string) (*Feed, error) {
	return b.mutateFeed(id, func(f *Feed) { f.DeskID = deskID })
}

func (b *boltBackend) RenameFeed(id, title string) (*Feed, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("title is required")
	}
	return b.mutateFeed(id, func(f *Feed) { f.Title, f.TitleOverride = title, title })
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
	var saved []*Article
	err := b.db.Update(func(tx *bolt.Tx) error {
		var err error
		saved, err = saveArticlesTx(tx, articles, preserveState)
		return err
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

func saveArticlesTx(tx *bolt.Tx, articles []*Article, preserveState bool) ([]*Article, error) {
	saved := make([]*Article, 0, len(articles))
	err := func() error {
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
				if old.FeedID != article.FeedID {
					return fmt.Errorf("%w: %s", ErrArticleFeedChanged, article.ID)
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
			if putErr := ab.Put([]byte(article.ID), data); putErr != nil {
				return putErr
			}

			if idxRoot != nil {
				fb, bucketErr := idxRoot.CreateBucketIfNotExists([]byte(article.FeedID))
				if bucketErr != nil {
					return bucketErr
				}
				if putErr := fb.Put([]byte(article.ID), []byte{1}); putErr != nil {
					return putErr
				}
			}

			if unreadErr := setUnreadMembership(tx, article.FeedID, article.ID, !article.Read); unreadErr != nil {
				return unreadErr
			}

			feedDates, err := tx.Bucket(articlesByFeedDateBucket).CreateBucketIfNotExists([]byte(article.FeedID))
			if err != nil {
				return err
			}
			for _, index := range []*bolt.Bucket{dateIdx, feedDates} {
				if hadPrev && !prevPublished.Equal(article.Published) {
					if err := index.Delete(makeDateIndexKey(prevPublished, article.ID)); err != nil {
						return err
					}
				}
				if err := index.Put(makeDateIndexKey(article.Published, article.ID), []byte(article.ID)); err != nil {
					return err
				}
			}
			saved = append(saved, &article)
		}
		return nil
	}()
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
	return b.GetArticlesWithCursorBounded(feedID, limit, cursor, 0)
}

func (b *boltBackend) GetArticlesWithCursorBounded(feedID string, limit int, cursor string, maxBytes int) ([]*Article, error) {
	var articles []*Article

	err := b.db.View(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		if ab == nil {
			return nil
		}

		if feedID != "" {
			return b.getArticlesForFeed(tx, ab, feedID, limit, cursor, maxBytes, &articles)
		}
		return b.getArticlesGlobalOptimized(tx, ab, limit, cursor, maxBytes, &articles)
	})

	if err != nil {
		return nil, err
	}
	return articles, nil
}

// getArticlesForFeed seeks directly into the per-feed chronological index.
// Only the cursor record and returned page are decoded, independent of feed size.
func (b *boltBackend) getArticlesForFeed(tx *bolt.Tx, ab *bolt.Bucket, feedID string, limit int, cursor string, maxBytes int, articles *[]*Article) error {
	root := tx.Bucket(articlesByFeedDateBucket)
	if root == nil {
		return nil
	}
	index := root.Bucket([]byte(feedID))
	if index == nil {
		return nil
	}
	if cursor != "" {
		raw := ab.Get([]byte(cursor))
		var article struct {
			FeedID string `json:"feed_id"`
		}
		if raw == nil || json.Unmarshal(raw, &article) != nil || article.FeedID != feedID {
			return nil
		}
	}
	return collectDatePage(ab, index, limit, cursor, maxBytes, articles)
}

// getArticlesGlobalOptimized retrieves articles across all feeds using the date index.
func (b *boltBackend) getArticlesGlobalOptimized(tx *bolt.Tx, ab *bolt.Bucket, limit int, cursor string, maxBytes int, articles *[]*Article) error {
	dateIdx := tx.Bucket(articlesByDateBucket)
	if dateIdx == nil {
		return fmt.Errorf("article date index missing")
	}
	return collectDatePage(ab, dateIdx, limit, cursor, maxBytes, articles)
}

func collectDatePage(ab, index *bolt.Bucket, limit int, cursor string, maxBytes int, articles *[]*Article) error {
	c := index.Cursor()
	count := 0
	payloadBytes := 2 // JSON array brackets

	k, articleID := seekDateCursor(ab, c, cursor)

	for ; k != nil && (limit <= 0 || count < limit); k, articleID = c.Next() {
		v := ab.Get(articleID)
		if v == nil {
			continue
		}
		payloadBytes += len(v) + 1 // serialized record and comma
		if maxBytes > 0 && payloadBytes > maxBytes {
			return ErrResultTooLarge
		}
		var article Article
		if err := json.Unmarshal(v, &article); err != nil {
			return err
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
		if dates := tx.Bucket(articlesByFeedDateBucket); dates != nil && dates.Bucket([]byte(id)) != nil {
			if err := dates.DeleteBucket([]byte(id)); err != nil {
				return err
			}
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
	// Flip the sign bit before inversion so negative seconds sort correctly.
	// UnixNano overflows for zero dates and dates outside 1678–2262.
	key := make([]byte, 12+len(articleID))
	binary.BigEndian.PutUint64(key[:8], ^(uint64(published.Unix()) ^ (uint64(1) << 63)))
	binary.BigEndian.PutUint32(key[8:12], ^uint32(published.Nanosecond()))
	copy(key[12:], articleID)
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
	// Cursor lookup needs metadata only; do not allocate/copy its content.
	var art struct {
		ID        string    `json:"id"`
		Published time.Time `json:"published"`
	}
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

func rebuildDateIndexesIfNeeded(tx *bolt.Tx) error {
	meta := tx.Bucket(metaBucket)
	if meta.Get(dateIndexFlag) != nil {
		return nil
	}
	for _, name := range [][]byte{articlesByDateBucket, articlesByFeedDateBucket, articlesByFeedBucket, articlesUnreadByFeedBucket} {
		if tx.Bucket(name) != nil {
			if err := tx.DeleteBucket(name); err != nil {
				return err
			}
		}
		if _, err := tx.CreateBucket(name); err != nil {
			return err
		}
	}
	if err := tx.Bucket(articlesBucket).ForEach(func(id, raw []byte) error {
		var article Article
		if err := json.Unmarshal(raw, &article); err != nil {
			return fmt.Errorf("rebuild article index %q: %w", id, err)
		}
		if article.ID != string(id) {
			return fmt.Errorf("rebuild article index: mismatched ID %q", id)
		}
		key := makeDateIndexKey(article.Published, article.ID)
		if err := tx.Bucket(articlesByDateBucket).Put(key, id); err != nil {
			return err
		}
		feedDates, err := tx.Bucket(articlesByFeedDateBucket).CreateBucketIfNotExists([]byte(article.FeedID))
		if err != nil {
			return err
		}
		if putErr := feedDates.Put(key, id); putErr != nil {
			return putErr
		}
		members, err := tx.Bucket(articlesByFeedBucket).CreateBucketIfNotExists([]byte(article.FeedID))
		if err != nil {
			return err
		}
		if err := members.Put(id, []byte{1}); err != nil {
			return err
		}
		return setUnreadMembership(tx, article.FeedID, article.ID, !article.Read)
	}); err != nil {
		return err
	}
	return meta.Put(dateIndexFlag, []byte{1})
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
