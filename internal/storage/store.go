package storage

import (
	"encoding/json"
	"fmt"
	"time"

	bolt "go.etcd.io/bbolt"
)

var (
	feedsBucket    = []byte("feeds")
	articlesBucket = []byte("articles")
	metaBucket     = []byte("metadata")
)

type Store struct {
	db *bolt.DB
}

func NewStore(dbPath string) (*Store, error) {
	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	err = db.Update(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{feedsBucket, articlesBucket, metaBucket} {
			if _, err := tx.CreateBucketIfNotExists(bucket); err != nil {
				return err
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
		return b.ForEach(func(k, v []byte) error {
			var feed Feed
			if err := json.Unmarshal(v, &feed); err != nil {
				return err
			}
			feeds = append(feeds, &feed)
			return nil
		})
	})
	return feeds, err
}

func (s *Store) SaveArticles(articles []*Article) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket(articlesBucket)
		for _, article := range articles {
			data, err := json.Marshal(article)
			if err != nil {
				return err
			}
			if err := b.Put([]byte(article.ID), data); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) GetArticles(feedID string, limit int) ([]*Article, error) {
	var articles []*Article
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(articlesBucket)
		c := b.Cursor()
		
		count := 0
		for k, v := c.Last(); k != nil && count < limit; k, v = c.Prev() {
			var article Article
			if err := json.Unmarshal(v, &article); err != nil {
				continue
			}
			if feedID == "" || article.FeedID == feedID {
				articles = append(articles, &article)
				count++
			}
		}
		return nil
	})
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
		feedBucket := tx.Bucket(feedsBucket)
		if err := feedBucket.Delete([]byte(id)); err != nil {
			return err
		}
		
		articleBucket := tx.Bucket(articlesBucket)
		c := articleBucket.Cursor()
		for k, v := c.First(); k != nil; k, v = c.Next() {
			var article Article
			if err := json.Unmarshal(v, &article); err != nil {
				continue
			}
			if article.FeedID == id {
				if err := c.Delete(); err != nil {
					return err
				}
			}
		}
		
		return nil
	})
}