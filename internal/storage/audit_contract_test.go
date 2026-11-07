package storage_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestAtomicFeedCommitContract(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			s := env.store
			original := &storage.Feed{ID: "f", URL: "https://example.com/rss", Title: "source", DeskID: "old", SubscribedURL: "https://example.com", SubscribedTitle: "original source", SubscribedAt: time.Unix(1, 0).UTC(), ETag: "old", LastFetched: time.Unix(2, 0).UTC()}
			article := &storage.Article{ID: "a", FeedID: "f", Title: "old", Read: true, Starred: true}
			if _, err := s.CommitFeed(original, []*storage.Article{article}, true); err != nil {
				t.Fatal(err)
			}
			if s.WriteGen() != 1 {
				t.Fatalf("create generation = %d", s.WriteGen())
			}
			if _, err := s.CommitFeed(original, []*storage.Article{{ID: "never", FeedID: "f"}}, true); !errors.Is(err, storage.ErrFeedExists) {
				t.Fatalf("duplicate = %v", err)
			}
			if _, err := s.GetArticle("never"); err == nil || s.WriteGen() != 1 {
				t.Fatal("duplicate commit changed storage")
			}
			if _, err := s.SetFeedDesk("f", "new desk"); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RenameFeed("f", "explicit title"); err != nil {
				t.Fatal(err)
			}
			refresh := *original // stale user fields must not overwrite current ones
			refresh.ETag, refresh.Title, refresh.LastFetched = "new", "new source", time.Unix(3, 0).UTC()
			incoming := &storage.Article{ID: "a", FeedID: "f", Title: "new"}
			saved, err := s.CommitFeed(&refresh, []*storage.Article{incoming}, false)
			if err != nil || len(saved) != 1 || !saved[0].Read || !saved[0].Starred || saved[0].Title != "new" {
				t.Fatalf("merged = %+v, %v", saved, err)
			}
			if incoming.Read || incoming.Starred || refresh.Title != "new source" {
				t.Fatal("commit mutated caller values")
			}
			current, err := s.GetFeed("f")
			if err != nil || current.DeskID != "new desk" || current.Title != "explicit title" || current.SubscribedURL != original.SubscribedURL || current.SubscribedTitle != original.SubscribedTitle || !current.SubscribedAt.Equal(original.SubscribedAt) || current.ETag != "new" {
				t.Fatalf("committed feed = %+v, %v", current, err)
			}
			if s.WriteGen() != 4 {
				t.Fatalf("refresh generation = %d", s.WriteGen())
			}
			before := *current
			refresh.ETag, refresh.LastFetched = "must rollback", time.Unix(4, 0).UTC()
			_, err = s.CommitFeed(&refresh, []*storage.Article{{ID: "partial", FeedID: "f"}, {ID: strings.Repeat("x", 32768), FeedID: "f"}}, false)
			if err == nil {
				t.Fatal("oversized index key accepted")
			}
			current, err = s.GetFeed("f")
			if err != nil || !reflect.DeepEqual(*current, before) || s.WriteGen() != 4 {
				t.Fatalf("failed transaction changed feed: %+v, %v, gen=%d", current, err, s.WriteGen())
			}
			if _, err := s.GetArticle("partial"); err == nil {
				t.Fatal("partial article escaped failed transaction")
			}
			if err := s.DeleteFeed("f"); err != nil {
				t.Fatal(err)
			}
			for _, mutate := range []func() error{
				func() error { _, e := s.SetFeedDesk("f", "x"); return e },
				func() error { _, e := s.RenameFeed("f", "x"); return e },
				func() error { _, e := s.CommitFeed(&refresh, nil, false); return e },
			} {
				if err := mutate(); err == nil {
					t.Fatal("mutation recreated deleted feed")
				}
			}
			if s.WriteGen() != 5 {
				t.Fatalf("failed mutations advanced generation: %d", s.WriteGen())
			}
		})
	}
}

func TestRejectArticleFeedReassignmentContract(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			s := env.store
			if err := s.SaveArticles([]*storage.Article{{ID: "a", FeedID: "owner", Published: time.Unix(-1, 0)}}); err != nil {
				t.Fatal(err)
			}
			for _, merge := range []bool{false, true} {
				batch := []*storage.Article{{ID: "partial", FeedID: "other"}, {ID: "a", FeedID: "other", Read: true, Published: time.Now()}}
				var err error
				if merge {
					_, err = s.MergeArticles(batch)
				} else {
					err = s.SaveArticles(batch)
				}
				if !errors.Is(err, storage.ErrArticleFeedChanged) {
					t.Fatalf("reassignment error = %v", err)
				}
				stats, statsErr := s.FeedStats()
				if statsErr != nil || stats["owner"].Total != 1 || stats["owner"].Unread != 1 || stats["other"].Total != 0 || s.WriteGen() != 1 {
					t.Fatalf("failed batch changed indexes: %+v, %v", stats, statsErr)
				}
				if _, getErr := s.GetArticle("partial"); getErr == nil {
					t.Fatal("failed batch saved its prefix")
				}
			}
		})
	}
}

func TestBoundedPageContract(t *testing.T) {
	for name, factory := range map[string]func(*testing.T) contractStore{"bolt": localContractStore, "rpc": rpcContractStore} {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			if err := env.store.SaveArticles([]*storage.Article{{ID: "a", FeedID: "f", Content: strings.Repeat("x", 1024)}}); err != nil {
				t.Fatal(err)
			}
			if _, err := env.store.GetArticlesWithCursorBounded("f", 1, "", 128); !errors.Is(err, storage.ErrResultTooLarge) {
				t.Fatalf("byte budget error = %v", err)
			}
			if got, err := env.store.GetArticlesWithCursorBounded("f", 1, "", 4096); err != nil || len(got) != 1 {
				t.Fatalf("bounded page = %v, %v", got, err)
			}
		})
	}
}
