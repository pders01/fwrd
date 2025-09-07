package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

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
	db *bolt.DB
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

func NewStore(dbPath string) (*Store, error) {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
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
		return nil, fmt.Errorf("creating buckets: %w", err)
	}

	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
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
			// Optimized feed-specific query using date index
			return s.getArticlesForFeedOptimized(tx, ab, feedID, limit, cursor, &articles)
		}

		// No feed specified: use date index for efficient sorted retrieval
		return s.getArticlesGlobalOptimized(tx, ab, limit, cursor, &articles)
	})

	return articles, err
}

// getArticlesForFeedOptimized efficiently retrieves articles for a specific feed using the date index
func (s *Store) getArticlesForFeedOptimized(tx *bolt.Tx, ab *bolt.Bucket, feedID string, limit int, cursor string, articles *[]*Article) error {
	// First, create a set of article IDs for the target feed for O(1) lookup
	feedArticleIDs := make(map[string]bool)

	idxRoot := tx.Bucket(articlesByFeedBucket)
	if idxRoot == nil {
		return nil // No feed index, no articles
	}

	fb := idxRoot.Bucket([]byte(feedID))
	if fb == nil {
		return nil // Feed not found
	}

	// Build lookup set of article IDs for this feed
	c := fb.Cursor()
	for k, _ := c.First(); k != nil; k, _ = c.Next() {
		feedArticleIDs[string(k)] = true
	}

	// Now iterate through date index (newest first) and filter by feed
	dateIdx := tx.Bucket(articlesByDateBucket)
	if dateIdx == nil {
		// Fallback: collect all articles for feed and sort manually
		return s.getArticlesForFeedFallback(tx, ab, feedID, limit, cursor, articles)
	}

	dateCursor := dateIdx.Cursor()
	count := 0

	// Position cursor after the last seen article if cursor is provided
	var k, articleID []byte
	if cursor != "" {
		// Find the cursor position
		found := false
		for k, articleID = dateCursor.First(); k != nil; k, articleID = dateCursor.Next() {
			if string(articleID) == cursor {
				found = true
				break
			}
		}
		if found {
			// Move to the next item after cursor
			k, articleID = dateCursor.Next()
		} else {
			// Cursor not found, start from beginning
			k, articleID = dateCursor.First()
		}
	} else {
		// No cursor, start from beginning
		k, articleID = dateCursor.First()
	}

	for ; k != nil && (limit <= 0 || count < limit); k, articleID = dateCursor.Next() {
		// Check if this article belongs to our target feed
		if !feedArticleIDs[string(articleID)] {
			continue
		}

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

// getArticlesForFeedFallback handles feed queries when date index is unavailable
func (s *Store) getArticlesForFeedFallback(tx *bolt.Tx, ab *bolt.Bucket, feedID string, limit int, cursor string, articles *[]*Article) error {
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

	// Position cursor after the last seen article if cursor is provided
	var k, articleID []byte
	if cursor != "" {
		// Find the cursor position
		found := false
		for k, articleID = c.First(); k != nil; k, articleID = c.Next() {
			if string(articleID) == cursor {
				found = true
				break
			}
		}
		if found {
			// Move to the next item after cursor
			k, articleID = c.Next()
		} else {
			// Cursor not found, start from beginning
			k, articleID = c.First()
		}
	} else {
		// No cursor, start from beginning
		k, articleID = c.First()
	}

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
		// Remove feed record
		feedBucket := tx.Bucket(feedsBucket)
		if err := feedBucket.Delete([]byte(id)); err != nil {
			return err
		}

		// Remove its articles using the index
		ab := tx.Bucket(articlesBucket)
		dateIdx := tx.Bucket(articlesByDateBucket)
		idxRoot := tx.Bucket(articlesByFeedBucket)

		var articleIDs [][]byte // Collect article IDs for date index cleanup

		if idxRoot != nil {
			fb := idxRoot.Bucket([]byte(id))
			if fb != nil {
				c := fb.Cursor()
				for k, _ := c.First(); k != nil; k, _ = c.Next() {
					articleIDs = append(articleIDs, append([]byte(nil), k...)) // Copy the key
					if ab != nil {
						_ = ab.Delete(k)
					}
				}
				// Delete the sub-bucket for this feed
				if err := idxRoot.DeleteBucket([]byte(id)); err != nil {
					// If bucket deletion fails, continue to avoid partial state
					_ = err
				}
			}
		}

		// Clean up date index entries for deleted articles
		if dateIdx != nil {
			for _, articleID := range articleIDs {
				// We need to find and remove entries from date index
				// Since we don't have the exact date key, we need to scan for entries with this article ID
				c := dateIdx.Cursor()
				for k, v := c.First(); k != nil; k, v = c.Next() {
					if bytes.Equal(v, articleID) {
						_ = dateIdx.Delete(k)
						break // Each article should only have one entry
					}
				}
			}
		}

		return nil
	})
}
