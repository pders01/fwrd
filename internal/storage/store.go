package storage

import (
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
)

type Store struct {
	db *bolt.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := bolt.Open(dbPath, 0o600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{feedsBucket, articlesBucket, metaBucket, articlesByFeedBucket} {
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
		for _, article := range articles {
			data, err := json.Marshal(article)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(article.ID), data); err != nil {
				return err
			}

			// Update index: ensure sub-bucket for this feed exists and record article ID
			if idxRoot != nil {
				fb, err := idxRoot.CreateBucketIfNotExists([]byte(article.FeedID))
				if err != nil {
					return err
				}
				if err := fb.Put([]byte(article.ID), []byte{1}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *Store) GetArticles(feedID string, limit int) ([]*Article, error) {
	var articles []*Article
	err := s.db.View(func(tx *bolt.Tx) error {
		ab := tx.Bucket(articlesBucket)
		if ab == nil {
			return nil
		}
		if feedID != "" {
			// Use index for specific feed
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
				articles = append(articles, &article)
			}
			return nil
		}

		// No feed specified: fall back to scanning all (could be optimized later)
		return ab.ForEach(func(_ []byte, v []byte) error {
			var article Article
			if err := json.Unmarshal(v, &article); err != nil {
				return nil
			}
			articles = append(articles, &article)
			return nil
		})
	})
	// Sort by Published date, newest first
	sort.Slice(articles, func(i, j int) bool {
		return articles[i].Published.After(articles[j].Published)
	})
	if limit > 0 && len(articles) > limit {
		articles = articles[:limit]
	}
	return articles, err
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
		idxRoot := tx.Bucket(articlesByFeedBucket)
		if idxRoot != nil {
			fb := idxRoot.Bucket([]byte(id))
			if fb != nil {
				c := fb.Cursor()
				for k, _ := c.First(); k != nil; k, _ = c.Next() {
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

		return nil
	})
}
