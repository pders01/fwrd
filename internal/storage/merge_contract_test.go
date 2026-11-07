package storage_test

import (
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestMergeArticlesPreservesLatestReaderState(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		for _, read := range []bool{false, true} {
			t.Run(name+map[bool]string{false: "/cleared", true: "/set"}[read], func(t *testing.T) {
				env := factory(t)
				defer env.cleanup()
				store := env.store
				original := &storage.Article{ID: "existing", FeedID: "feed", Title: "Old", Read: !read, Starred: !read, Published: time.Unix(1, 0)}
				if err := store.SaveArticles([]*storage.Article{original}); err != nil {
					t.Fatal(err)
				}
				// Simulate a fetch carrying stale flags, followed by user actions
				// committed after it began but before the fetched content is saved.
				fetched := &storage.Article{ID: "existing", FeedID: "feed", Title: "Updated", Read: !read, Starred: !read, Published: time.Unix(2, 0)}
				if err := store.MarkArticleRead("existing", read); err != nil {
					t.Fatal(err)
				}
				if err := store.MarkArticleStarred("existing", read); err != nil {
					t.Fatal(err)
				}
				gen := store.WriteGen()
				saved, err := store.MergeArticles([]*storage.Article{fetched, {ID: "new", FeedID: "feed", Read: true, Published: time.Unix(3, 0)}})
				if err != nil {
					t.Fatal(err)
				}
				if len(saved) != 2 || saved[0].Read != read || saved[0].Starred != read || !saved[1].Read {
					t.Fatalf("merged records = %#v", saved)
				}
				if fetched.Read != !read || fetched.Starred != !read {
					t.Fatal("merge mutated caller's stale snapshot")
				}
				stored, err := store.GetArticle("existing")
				if err != nil || stored.Read != read || stored.Starred != read || stored.Title != "Updated" || !stored.Published.Equal(fetched.Published) {
					t.Fatalf("stored record = %#v, %v", stored, err)
				}
				stats, err := store.FeedStats()
				wantUnread := 1
				if read {
					wantUnread = 0
				}
				if err != nil || stats["feed"].Unread != wantUnread || stats["feed"].Total != 2 {
					t.Fatalf("merged stats = %#v, %v", stats, err)
				}
				page, err := store.GetArticlesWithCursor("", 10, "")
				if err != nil || len(page) != 2 || page[0].ID != "new" || page[1].ID != "existing" {
					t.Fatalf("merged date index = %#v, %v", page, err)
				}
				if store.WriteGen() != gen+1 {
					t.Fatal("merge did not invalidate derived caches exactly once")
				}
			})
		}
	}
}

func TestMergeArticlesRollsBackWithoutChangingInputsOrGeneration(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			store := env.store
			if err := store.SaveArticles([]*storage.Article{{ID: "existing", FeedID: "feed", Title: "Old", Read: true}}); err != nil {
				t.Fatal(err)
			}
			input := &storage.Article{ID: "existing", FeedID: "feed", Title: "New", Read: false}
			gen := store.WriteGen()
			saved, err := store.MergeArticles([]*storage.Article{input, nil})
			if err == nil || saved != nil {
				t.Fatalf("invalid merge = %#v, %v", saved, err)
			}
			got, err := store.GetArticle("existing")
			if err != nil || got.Title != "Old" || !got.Read {
				t.Fatalf("failed merge changed database: %#v, %v", got, err)
			}
			if input.Read || input.Title != "New" || store.WriteGen() != gen {
				t.Fatal("failed merge changed inputs or generation")
			}
		})
	}
}
