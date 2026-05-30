package storage

import (
	"encoding/json"
	"os"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// Quantifies the headroom for a count-only fix on /feeds. Run against a copy
// of a real DB:
//
//	FWRD_BENCH_DB=/tmp/fwrd-bench.db go test ./internal/storage -run x \
//	    -bench 'BenchmarkCount' -benchmem
//
// CountDecodeAll mirrors today's feedCounts (decode + tally every article in
// every feed). CountKeysOnly shows totals via per-feed bucket KeyN with zero
// decode. CountUnreadDecodeOnce decodes each article exactly once (single tx,
// no per-feed sort) to get unread — the no-schema-change middle ground.
func benchDB(b *testing.B) *Store {
	b.Helper()
	path := os.Getenv("FWRD_BENCH_DB")
	if path == "" {
		b.Skip("set FWRD_BENCH_DB to a populated DB copy")
	}
	store, err := NewStore(path)
	if err != nil {
		b.Fatalf("open: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	return store
}

// BenchmarkCountDecodeAll = current behaviour: GetArticles(feedID, 0) per feed.
func BenchmarkCountDecodeAll(b *testing.B) {
	s := benchDB(b)
	feeds, _ := s.GetAllFeeds()
	for b.Loop() {
		for _, f := range feeds {
			arts, _ := s.GetArticles(f.ID, 0)
			var unread int
			for _, a := range arts {
				if !a.Read {
					unread++
				}
			}
			_ = unread
		}
	}
}

// BenchmarkCountKeysOnly = totals via KeyN, no JSON decode at all.
func BenchmarkCountKeysOnly(b *testing.B) {
	s := benchDB(b)
	for b.Loop() {
		_ = s.db.View(func(tx *bolt.Tx) error {
			idxRoot := tx.Bucket(articlesByFeedBucket)
			if idxRoot == nil {
				return nil
			}
			return idxRoot.ForEach(func(feedID, _ []byte) error {
				if fb := idxRoot.Bucket(feedID); fb != nil {
					_ = fb.Stats().KeyN // total, no decode
				}
				return nil
			})
		})
	}
}

// BenchmarkCountUnreadDecodeOnce = single tx, decode every article once,
// tally unread by FeedID. No per-feed sort, no repeated transactions.
func BenchmarkCountUnreadDecodeOnce(b *testing.B) {
	s := benchDB(b)
	for b.Loop() {
		counts := map[string]int{}
		_ = s.db.View(func(tx *bolt.Tx) error {
			ab := tx.Bucket(articlesBucket)
			if ab == nil {
				return nil
			}
			return ab.ForEach(func(_, v []byte) error {
				var a struct {
					FeedID string `json:"feed_id"`
					Read   bool   `json:"read"`
				}
				if json.Unmarshal(v, &a) == nil && !a.Read {
					counts[a.FeedID]++
				}
				return nil
			})
		})
		_ = counts
	}
}
