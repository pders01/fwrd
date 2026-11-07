package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

func dateFixture() []*Article {
	return []*Article{
		{ID: "zero", FeedID: "f", Published: time.Time{}},
		{ID: "old", FeedID: "f", Published: time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "negative", FeedID: "f", Published: time.Unix(-1, 999999999).UTC()},
		{ID: "epoch", FeedID: "f", Published: time.Unix(0, 0).UTC()},
		{ID: "a", FeedID: "f", Published: time.Unix(1, 0).UTC()},
		{ID: "b", FeedID: "f", Published: time.Unix(1, 0).UTC()},
		{ID: "nano", FeedID: "f", Published: time.Unix(1, 1).UTC()},
		{ID: "future", FeedID: "f", Published: time.Date(2500, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "last", FeedID: "f", Published: time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC)},
	}
}

func assertDatePages(t *testing.T, s *Store) {
	t.Helper()
	want := []string{"last", "future", "nano", "a", "b", "epoch", "negative", "old", "zero"}
	for _, feedID := range []string{"", "f"} {
		var got []string
		cursor := ""
		for range len(want) + 1 {
			page, err := s.GetArticlesWithCursor(feedID, 1, cursor)
			if err != nil {
				t.Fatal(err)
			}
			if len(page) == 0 {
				break
			}
			got = append(got, page[0].ID)
			cursor = page[0].ID
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("feed %q chronology = %v, want %v", feedID, got, want)
		}
		page, err := s.GetArticlesWithCursor(feedID, 2, "missing")
		if err != nil || len(page) != 0 {
			t.Fatalf("missing cursor = %v, %v", page, err)
		}
	}
}

func TestSignedDateIndexesAndUpdates(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	if err := s.SaveArticles(dateFixture()); err != nil {
		t.Fatal(err)
	}
	assertDatePages(t, s)
	if err := s.SaveArticles([]*Article{{ID: "outsider", FeedID: "other"}}); err != nil {
		t.Fatal(err)
	}
	if page, err := s.GetArticlesWithCursor("f", 1, "outsider"); err != nil || len(page) != 0 {
		t.Fatalf("foreign cursor = %v, %v", page, err)
	}
	if err := s.SaveArticles([]*Article{{ID: "zero", FeedID: "f", Published: time.Date(9999, 12, 31, 0, 0, 1, 0, time.UTC)}}); err != nil {
		t.Fatal(err)
	}
	for _, feedID := range []string{"", "f"} {
		all, err := s.GetArticles(feedID, 0)
		if err != nil || all[0].ID != "zero" {
			t.Fatalf("updated ordering = %v, %v", all, err)
		}
		seen := 0
		for _, a := range all {
			if a.ID == "zero" {
				seen++
			}
		}
		if seen != 1 {
			t.Fatalf("stale date index survived update: %d", seen)
		}
	}
}

func createLegacyIndexDB(t *testing.T, path string, corrupt bool) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if updateErr := db.Update(func(tx *bolt.Tx) error {
		for _, name := range [][]byte{feedsBucket, articlesBucket, metaBucket, articlesByDateBucket, articlesByFeedBucket, articlesUnreadByFeedBucket} {
			if _, createErr := tx.CreateBucket(name); createErr != nil {
				return createErr
			}
		}
		for _, article := range dateFixture() {
			data, marshalErr := json.Marshal(article)
			if marshalErr != nil {
				return marshalErr
			}
			if putErr := tx.Bucket(articlesBucket).Put([]byte(article.ID), data); putErr != nil {
				return putErr
			}
		}
		if corrupt {
			if putErr := tx.Bucket(articlesBucket).Put([]byte("corrupt"), []byte("{")); putErr != nil {
				return putErr
			}
		}
		// Incomplete/stale pre-migration indexes must not be trusted, even if
		// their old metadata marker claims the unread backfill completed.
		if putErr := tx.Bucket(articlesByDateBucket).Put([]byte("legacy"), []byte("ghost")); putErr != nil {
			return putErr
		}
		return tx.Bucket(metaBucket).Put(unreadIndexFlag, []byte{1})
	}); updateErr != nil {
		t.Fatal(updateErr)
	}
}

func TestDateIndexMigrationRebuildAndReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	createLegacyIndexDB(t, path, false)
	for range 2 {
		s, err := NewStore(path)
		if err != nil {
			t.Fatal(err)
		}
		assertDatePages(t, s)
		stats, statsErr := s.FeedStats()
		if statsErr != nil || stats["f"].Total != 9 || stats["f"].Unread != 9 {
			t.Fatalf("migrated membership = %+v, %v", stats, statsErr)
		}
		if closeErr := s.Close(); closeErr != nil {
			t.Fatal(closeErr)
		}
	}
}

func TestFailedIndexMigrationRollsBackAndCanRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	createLegacyIndexDB(t, path, true)
	if s, err := NewStore(path); err == nil {
		_ = s.Close()
		t.Fatal("corrupt record silently accepted by migration")
	}
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	if updateErr := db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(metaBucket).Get(dateIndexFlag) != nil || tx.Bucket(articlesByFeedDateBucket) != nil || string(tx.Bucket(articlesByDateBucket).Get([]byte("legacy"))) != "ghost" {
			return fmt.Errorf("failed migration left partial indexes")
		}
		return tx.Bucket(articlesBucket).Delete([]byte("corrupt"))
	}); updateErr != nil {
		t.Fatal(updateErr)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	assertDatePages(t, s)
}

// newPageSizeStore exercises the 4 KiB and 16 KiB database layouts without
// depending on the host OS page size. Reopening preserves the stored page size.
func newPageSizeStore(t testing.TB, pageSize int) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pages.db")
	db, err := bolt.Open(path, 0o600, &bolt.Options{PageSize: pageSize})
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := db.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return s
}

func pageDecodeFixture(count int) []*Article {
	articles := make([]*Article, count)
	for i := range articles {
		articles[i] = &Article{ID: fmt.Sprintf("a-%05d", i), FeedID: "f", Published: time.Unix(int64(i), 0)}
	}
	return articles
}

func TestArticlePagesDoNotDecodeOutsideRequestedPage(t *testing.T) {
	const total = 4000
	cases := []struct {
		name   string
		cursor string
		first  int
	}{
		{name: "first", first: total - 1},
		{name: "middle", cursor: "a-02000", first: 1999},
		{name: "deep", cursor: "a-00009", first: 8},
	}
	for _, pageSize := range []int{4096, 16384} {
		t.Run(fmt.Sprintf("page_size_%d", pageSize), func(t *testing.T) {
			s := newPageSizeStore(t, pageSize)
			articles := pageDecodeFixture(total)
			if err := s.SaveArticles(articles); err != nil {
				t.Fatal(err)
			}
			// For each query, only its cursor and five page records remain
			// decodable. Same-length corruption preserves record sizes;
			// eager decoding must not be needed to find the requested page.
			for _, tc := range cases {
				keep := map[string]bool{tc.cursor: true}
				for offset := range 5 {
					keep[articles[tc.first-offset].ID] = true
				}
				if err := s.backend.(*boltBackend).db.Update(func(tx *bolt.Tx) error {
					ab := tx.Bucket(articlesBucket)
					for _, article := range articles {
						data, marshalErr := json.Marshal(article)
						if marshalErr != nil {
							return marshalErr
						}
						if !keep[article.ID] {
							data = bytes.Repeat([]byte{'!'}, len(data))
						}
						if putErr := ab.Put([]byte(article.ID), data); putErr != nil {
							return putErr
						}
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for _, feedID := range []string{"f", ""} {
					t.Run(fmt.Sprintf("feed_%q/%s", feedID, tc.name), func(t *testing.T) {
						page, err := s.GetArticlesWithCursor(feedID, 5, tc.cursor)
						if err != nil || len(page) != 5 {
							t.Fatalf("page = %v, %v", page, err)
						}
						for offset, article := range page {
							if want := articles[tc.first-offset].ID; article.ID != want {
								t.Errorf("article %d = %q, want %q", offset, article.ID, want)
							}
						}
						// Expanding the page must encounter the corrupt next
						// record, not silently skip it and scan for a later one.
						_, err = s.GetArticlesWithCursor(feedID, 6, tc.cursor)
						var syntaxErr *json.SyntaxError
						if !errors.As(err, &syntaxErr) {
							t.Fatalf("expanded page error = %v, want JSON syntax error", err)
						}
					})
				}
			}
		})
	}
}

// Allocation totals include B-tree cursor stacks, which depend on page size
// and tree depth even when only a fixed number of articles are decoded. Keep
// those totals visible as benchmarks rather than a platform-specific CI gate.
func BenchmarkFeedPageDecodeAllocations(b *testing.B) {
	for _, pageSize := range []int{4096, 16384} {
		for _, total := range []int{20, 4000} {
			b.Run(fmt.Sprintf("page_size_%d/articles_%d", pageSize, total), func(b *testing.B) {
				s := newPageSizeStore(b, pageSize)
				if err := s.SaveArticles(pageDecodeFixture(total)); err != nil {
					b.Fatal(err)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					page, err := s.GetArticlesWithCursor("f", 5, "a-00009")
					if err != nil || len(page) != 5 {
						b.Fatalf("page = %v, %v", page, err)
					}
				}
			})
		}
	}
}
