package storage

import (
	"encoding/json"
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

func TestFeedPageDecodeAllocationsDoNotScaleWithFeedSize(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	appendArticles := func(start, end int) {
		t.Helper()
		articles := make([]*Article, 0, end-start)
		for i := start; i < end; i++ {
			articles = append(articles, &Article{ID: fmt.Sprintf("a-%05d", i), FeedID: "f", Published: time.Unix(int64(i), 0)})
		}
		if err := s.SaveArticles(articles); err != nil {
			t.Fatal(err)
		}
	}
	measure := func() float64 {
		return testing.AllocsPerRun(5, func() {
			page, err := s.GetArticlesWithCursor("f", 5, "a-00009")
			if err != nil || len(page) != 5 {
				t.Fatalf("page = %v, %v", page, err)
			}
		})
	}
	appendArticles(0, 20)
	small := measure()
	appendArticles(20, 4000)
	large := measure()
	if large > small+30 {
		t.Fatalf("page allocations scaled with feed size: small %.0f, large %.0f", small, large)
	}
}
