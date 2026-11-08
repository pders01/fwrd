package storage_test

import (
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestCountedFeedCommitContract(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			s := env.store
			f := &storage.Feed{ID: "f", URL: "https://example.test/feed", ETag: "saved", LastFetched: time.Unix(1, 0).UTC()}
			incoming := []*storage.Article{{ID: "a", FeedID: "f"}, {ID: "b", FeedID: "f"}, {ID: "a", FeedID: "f", Title: "last duplicate"}}
			for i, want := range []int{2, 0, 1} {
				if i == 2 {
					incoming = append(incoming, &storage.Article{ID: "c", FeedID: "f"})
				}
				result, err := s.CommitFeedCounted(f, incoming, i == 0)
				if err != nil || result.AddedArticles != want || len(result.Articles) != len(incoming) {
					t.Fatalf("commit %d = %+v, %v; want added=%d merged=%d", i, result, err, want, len(incoming))
				}
			}
			before, err := s.GetFeed(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			gen := s.WriteGen()
			f.ETag = "tentative"
			result, err := s.CommitFeedCounted(f, []*storage.Article{{ID: "partial", FeedID: "f"}, {ID: strings.Repeat("x", 40*1024), FeedID: "f"}}, false)
			if err == nil || result.AddedArticles != 0 || result.Articles != nil || s.WriteGen() != gen {
				t.Fatalf("failed commit = %+v, %v, gen=%d", result, err, s.WriteGen())
			}
			after, err := s.GetFeed(f.ID)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("rollback metadata = %+v, %v", after, err)
			}
			if _, getErr := s.GetArticle("partial"); getErr == nil {
				t.Fatal("partial insertion survived rollback")
			}
			// The rolled-back ID remains new on a later successful commit.
			result, err = s.CommitFeedCounted(before, []*storage.Article{{ID: "partial", FeedID: "f"}}, false)
			if err != nil || result.AddedArticles != 1 {
				t.Fatalf("after rollback = %+v, %v", result, err)
			}
		})
	}
}

func TestCountedFeedCommitConcurrentWriters(t *testing.T) {
	env := localContractStore(t)
	defer env.cleanup()
	s := env.store
	f := &storage.Feed{ID: "f", URL: "https://example.test/feed"}
	if err := s.SaveFeed(f); err != nil {
		t.Fatal(err)
	}
	const writers = 12
	results := make(chan storage.FeedCommitResult, writers)
	errors := make(chan error, writers)
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			result, err := s.CommitFeedCounted(f, []*storage.Article{{ID: "shared", FeedID: "f"}, {ID: "shared", FeedID: "f"}}, false)
			results <- result
			errors <- err
		})
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	added := 0
	for result := range results {
		added += result.AddedArticles
		if len(result.Articles) != 2 {
			t.Fatalf("lost merged records: %+v", result)
		}
	}
	if added != 1 {
		t.Fatalf("concurrent insertion total = %d, want 1", added)
	}
}

func TestFeedHealthUpdateContract(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			s := env.store
			f := &storage.Feed{ID: "f", URL: "https://example.test/feed", ETag: "committed", LastFetched: time.Unix(1, 0).UTC()}
			if err := s.SaveFeed(f); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RenameFeed(f.ID, "Current title"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.SetFeedDesk(f.ID, "current-desk"); err != nil {
				t.Fatal(err)
			}
			before, err := s.GetFeed(f.ID)
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC()
			got, err := s.SetFeedError(f.ID, "commit failed", at)
			if err != nil || got.LastError != "commit failed" || !got.LastErrorAt.Equal(at) {
				t.Fatalf("health = %+v, %v", got, err)
			}
			got.LastError, got.LastErrorAt = before.LastError, before.LastErrorAt
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("health update changed committed metadata: %+v, want %+v", got, before)
			}
			if _, err := s.SetFeedError("missing", "failure", at); err == nil {
				t.Fatal("health update recreated missing feed")
			}
		})
	}
}
