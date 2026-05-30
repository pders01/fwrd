package storage

import (
	"os"
	"path/filepath"
	"testing"

	bolt "go.etcd.io/bbolt"
)

// TestFeedStats_ParityWithDecode cross-checks the index-based counts against a
// full decode of every article on a real database. Gated on FWRD_BENCH_DB so
// it skips in CI; run it after migrating a real DB to confirm the rebuild and
// the maintained index agree with the ground truth.
func TestFeedStats_ParityWithDecode(t *testing.T) {
	path := os.Getenv("FWRD_BENCH_DB")
	if path == "" {
		t.Skip("set FWRD_BENCH_DB to a populated DB copy")
	}
	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	indexed, err := store.FeedStats()
	if err != nil {
		t.Fatalf("FeedStats: %v", err)
	}

	// Ground truth: decode every article and tally by feed.
	want := map[string]FeedStat{}
	feeds, _ := store.GetAllFeeds()
	for _, f := range feeds {
		arts, _ := store.GetArticles(f.ID, 0)
		st := FeedStat{Total: len(arts)}
		for _, a := range arts {
			if !a.Read {
				st.Unread++
			}
		}
		if st.Total > 0 {
			want[f.ID] = st
		}
	}

	for id, w := range want {
		if g := indexed[id]; g != w {
			t.Errorf("feed %s: index=%+v decode=%+v", id, g, w)
		}
	}
}

func art(id, feedID string, read bool) *Article {
	return &Article{ID: id, FeedID: feedID, Title: id, Read: read}
}

// TestFeedStats_CountsAndUnreadTransitions verifies that FeedStats reports
// correct totals and unread counts from the index, and that marking read /
// unread and re-saving keep the unread index in sync.
func TestFeedStats_CountsAndUnreadTransitions(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	if err := store.SaveArticles([]*Article{
		art("a1", "f1", false),
		art("a2", "f1", false),
		art("a3", "f1", true),
		art("b1", "f2", false),
	}); err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}

	stats, err := store.FeedStats()
	if err != nil {
		t.Fatalf("FeedStats: %v", err)
	}
	if got := stats["f1"]; got.Total != 3 || got.Unread != 2 {
		t.Fatalf("f1 = %+v, want {Unread:2 Total:3}", got)
	}
	if got := stats["f2"]; got.Total != 1 || got.Unread != 1 {
		t.Fatalf("f2 = %+v, want {Unread:1 Total:1}", got)
	}

	// Mark one of f1's unread articles read: unread drops to 1.
	if err := store.MarkArticleRead("a1", true); err != nil {
		t.Fatalf("MarkArticleRead: %v", err)
	}
	stats, _ = store.FeedStats()
	if got := stats["f1"]; got.Unread != 1 || got.Total != 3 {
		t.Fatalf("after read, f1 = %+v, want {Unread:1 Total:3}", got)
	}

	// Mark it unread again: unread back to 2.
	if err := store.MarkArticleRead("a1", false); err != nil {
		t.Fatalf("MarkArticleRead unread: %v", err)
	}
	stats, _ = store.FeedStats()
	if got := stats["f1"].Unread; got != 2 {
		t.Fatalf("after unread, f1 unread = %d, want 2", got)
	}

	// Re-saving an article as read removes it from the unread index without
	// a separate mark call (idempotent membership).
	if err := store.SaveArticles([]*Article{art("a2", "f1", true)}); err != nil {
		t.Fatalf("re-save read: %v", err)
	}
	stats, _ = store.FeedStats()
	if got := stats["f1"].Unread; got != 1 {
		t.Fatalf("after re-save read, f1 unread = %d, want 1", got)
	}

	// Starring must not touch the unread count.
	if err := store.MarkArticleStarred("a1", true); err != nil {
		t.Fatalf("MarkArticleStarred: %v", err)
	}
	stats, _ = store.FeedStats()
	if got := stats["f1"].Unread; got != 1 {
		t.Fatalf("after star, f1 unread = %d, want 1", got)
	}
}

// TestFeedStats_DeleteFeedClearsIndex confirms a deleted feed disappears from
// both the total and unread index.
func TestFeedStats_DeleteFeedClearsIndex(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	store.SaveArticles([]*Article{art("a1", "f1", false), art("b1", "f2", false)})
	if err := store.DeleteFeed("f1"); err != nil {
		t.Fatalf("DeleteFeed: %v", err)
	}
	stats, _ := store.FeedStats()
	if _, ok := stats["f1"]; ok {
		t.Fatalf("f1 still present after delete: %+v", stats["f1"])
	}
	if got := stats["f2"]; got.Total != 1 || got.Unread != 1 {
		t.Fatalf("f2 = %+v, want {Unread:1 Total:1}", got)
	}
}

// TestUnreadIndex_RebuildOnOpen simulates upgrading a database that predates
// the unread index: the index sub-buckets and the build flag are wiped, then
// the store is reopened. buildUnreadIndexIfNeeded must reconstruct the index
// from the article records so FeedStats is correct without any new writes.
func TestUnreadIndex_RebuildOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rebuild.db")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	store.SaveArticles([]*Article{
		art("a1", "f1", false),
		art("a2", "f1", true),
		art("b1", "f2", false),
	})
	if closeErr := store.Close(); closeErr != nil {
		t.Fatalf("Close: %v", closeErr)
	}

	// Wipe the unread index and the build flag to mimic a pre-index DB.
	db, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		if tx.Bucket(articlesUnreadByFeedBucket) != nil {
			if delErr := tx.DeleteBucket(articlesUnreadByFeedBucket); delErr != nil {
				return delErr
			}
		}
		return tx.Bucket(metaBucket).Delete(unreadIndexFlag)
	})
	if err != nil {
		t.Fatalf("wipe index: %v", err)
	}
	db.Close()

	// Reopen via NewStore: the rebuild must run.
	store2, err := NewStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer store2.Close()

	stats, err := store2.FeedStats()
	if err != nil {
		t.Fatalf("FeedStats: %v", err)
	}
	if got := stats["f1"]; got.Total != 2 || got.Unread != 1 {
		t.Fatalf("rebuilt f1 = %+v, want {Unread:1 Total:2}", got)
	}
	if got := stats["f2"].Unread; got != 1 {
		t.Fatalf("rebuilt f2 unread = %d, want 1", got)
	}
}

// TestWriteGen_AdvancesOnMutations confirms the write generation strictly
// increases on each mutation (the front-page cache relies on this) and stays
// put across read-only calls.
func TestWriteGen_AdvancesOnMutations(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	g0 := store.WriteGen()
	store.SaveFeed(&Feed{ID: "f1", URL: "https://example.com/feed"})
	g1 := store.WriteGen()
	if g1 <= g0 {
		t.Fatalf("SaveFeed did not advance WriteGen: %d -> %d", g0, g1)
	}
	store.SaveArticles([]*Article{art("a1", "f1", false)})
	g2 := store.WriteGen()
	if g2 <= g1 {
		t.Fatalf("SaveArticles did not advance WriteGen: %d -> %d", g1, g2)
	}
	store.MarkArticleRead("a1", true)
	g3 := store.WriteGen()
	if g3 <= g2 {
		t.Fatalf("MarkArticleRead did not advance WriteGen: %d -> %d", g2, g3)
	}

	// Read-only call leaves it unchanged.
	store.FeedStats()
	if store.WriteGen() != g3 {
		t.Fatalf("FeedStats advanced WriteGen, want stable at %d", g3)
	}
}
