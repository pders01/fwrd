package control

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

type countedRPCListener struct {
	mu      sync.Mutex
	batches [][]*storage.Article
}

func (l *countedRPCListener) OnDataUpdated(_ *storage.Feed, articles []*storage.Article) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.batches = append(l.batches, articles)
}

func (l *countedRPCListener) snapshot() [][]*storage.Article {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([][]*storage.Article(nil), l.batches...)
}

func TestRPCRefreshCountsOnlyUniqueInsertions(t *testing.T) {
	for _, mode := range []string{"owner-manager", "manager-with-rpc-store"} {
		t.Run(mode, func(t *testing.T) {
			var generation, requests atomic.Int32
			fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>Feed</title>`)
				ids := []string{"a", "b", "a"}
				if generation.Load() == 2 {
					ids = append(ids, "c")
				}
				for _, id := range ids {
					_, _ = fmt.Fprintf(w, `<item><title>generation %d</title><guid>%s</guid></item>`, generation.Load()/2, id)
				}
				_, _ = fmt.Fprint(w, `</channel></rss>`)
			}))
			defer fixture.Close()
			cfg := config.TestConfig()
			cfg.Feed.RefreshInterval = 0
			cfg.Feed.AllowPrivateNetworks = true
			listener := &countedRPCListener{}
			store, _, ep := startRefreshRPC(t, func(store *storage.Store) FeedOperations {
				manager := feed.NewManager(store, cfg)
				manager.RegisterDataListener(listener)
				return &LocalOps{Store: store, Manager: manager}
			})
			if err := store.SaveFeed(&storage.Feed{ID: "f", URL: fixture.URL}); err != nil {
				t.Fatal(err)
			}
			client := refreshClient(t, ep)
			remoteStore := storage.NewRPCStore(client)
			refresh := func() (feed.RefreshSummary, error) {
				return newRemoteOps(client, cfg).RefreshAllFeeds(context.Background(), true)
			}
			if mode == "manager-with-rpc-store" {
				manager := feed.NewManager(remoteStore, cfg)
				refresh = manager.RefreshAllFeeds
			}
			for i, want := range []int{2, 0, 1} {
				generation.Store(int32(i))
				summary, err := refresh()
				if err != nil || summary.AddedArticles != want || summary.UpdatedFeeds != 1 || len(summary.Errors) != 0 {
					t.Fatalf("refresh %d = %+v, %v; want added=%d", i, summary, err, want)
				}
				batches := listener.snapshot()
				if len(batches) != i+1 || len(batches[i]) != 3+i/2 {
					t.Fatalf("lost merged listener records: %+v", batches)
				}
				for _, article := range batches[i] {
					if article.Title != fmt.Sprintf("generation %d", i/2) || (i > 0 && article.ID == "f:a" && (!article.Read || !article.Starred)) {
						t.Fatalf("stale merged content/state: %+v", article)
					}
				}
				if i == 0 {
					if err := remoteStore.MarkArticleRead("f:a", true); err != nil {
						t.Fatal(err)
					}
					if err := remoteStore.MarkArticleStarred("f:a", true); err != nil {
						t.Fatal(err)
					}
				}
			}
			if requests.Load() != 3 {
				t.Fatalf("HTTP requests replayed: %d", requests.Load())
			}
		})
	}
}

func TestRPCRefreshCommitFailureHealthAndRecovery(t *testing.T) {
	var broken atomic.Bool
	broken.Store(true)
	var requests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		guid, etag := "stable", "recovered"
		if broken.Load() {
			guid, etag = strings.Repeat("g", 40*1024), "tentative"
		}
		w.Header().Set("ETag", etag)
		_, _ = fmt.Fprintf(w, `<rss version="2.0"><channel><title>Tentative title</title><item><title>Article</title><guid>%s</guid></item></channel></rss>`, guid)
	}))
	defer fixture.Close()
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 0
	cfg.Feed.AllowPrivateNetworks = true
	listener := &countedRPCListener{}
	store, _, ep := startRefreshRPC(t, func(store *storage.Store) FeedOperations {
		manager := feed.NewManager(store, cfg)
		manager.RegisterDataListener(listener)
		return &LocalOps{Store: store, Manager: manager}
	})
	before := &storage.Feed{ID: "f", URL: fixture.URL, Title: "Saved title", DeskID: "saved-desk", ETag: "saved", LastFetched: time.Unix(1, 0).UTC(), UpdatedAt: time.Unix(2, 0).UTC()}
	if _, err := store.CommitFeed(before, []*storage.Article{{ID: "f:stable", FeedID: "f", Read: true, Starred: true}}, true); err != nil {
		t.Fatal(err)
	}
	client := refreshClient(t, ep)
	remote := newRemoteOps(client, cfg)
	remoteStore := storage.NewRPCStore(client)
	for i := range 2 {
		summary, err := remote.RefreshAllFeeds(context.Background(), true)
		if err == nil || summary.UpdatedFeeds != 0 || summary.AddedArticles != 0 || len(summary.Errors) != 1 || !strings.Contains(err.Error(), "saving refreshed feed:") {
			t.Fatalf("failed refresh %d = %+v, %v", i, summary, err)
		}
		got, getErr := remoteStore.GetFeed("f")
		if getErr != nil || got.LastError == "" || got.LastErrorAt.IsZero() || !strings.Contains(err.Error(), got.LastError) || got.ETag != before.ETag || !got.LastFetched.Equal(before.LastFetched) || !got.UpdatedAt.Equal(before.UpdatedAt) || got.Title != before.Title || got.DeskID != before.DeskID {
			t.Fatalf("failed RPC commit health/metadata = %+v, %v", got, getErr)
		}
		if len(listener.snapshot()) != 0 {
			t.Fatal("failed RPC refresh notified listeners")
		}
	}
	broken.Store(false)
	summary, err := remote.RefreshAllFeeds(context.Background(), true)
	if err != nil || summary.UpdatedFeeds != 1 || summary.AddedArticles != 0 || len(listener.snapshot()) != 1 {
		t.Fatalf("recovery = %+v, %v", summary, err)
	}
	got, err := remoteStore.GetFeed("f")
	if err != nil || got.LastError != "" || !got.LastErrorAt.IsZero() || got.ETag != "recovered" || !got.LastFetched.After(before.LastFetched) {
		t.Fatalf("recovered health = %+v, %v", got, err)
	}
	articles, err := remoteStore.GetArticles("f", 0)
	if err != nil || len(articles) != 1 || !articles[0].Read || !articles[0].Starred {
		t.Fatalf("reader state = %+v, %v", articles, err)
	}
	if requests.Load() != 3 {
		t.Fatalf("failed refresh replayed: requests=%d", requests.Load())
	}
}
