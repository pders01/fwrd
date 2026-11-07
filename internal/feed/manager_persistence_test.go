package feed

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func persistenceManager(t *testing.T) (*Manager, *storage.Store) {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	cfg := config.TestConfig()
	cfg.Feed.AllowPrivateNetworks = true // explicitly allow this test's canary
	cfg.Feed.RefreshInterval = 0
	return NewManager(store, cfg), store
}

func persistenceFeed(guid string) string {
	return fmt.Sprintf(`<?xml version="1.0"?><rss version="2.0"><channel><title>Feed</title><item><title>Article</title><link>https://example.com/article</link><guid>%s</guid></item></channel></rss>`, guid)
}

func TestFailedSubscriptionDoesNotCommitValidators(t *testing.T) {
	manager, store := persistenceManager(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("failed subscription persisted conditional request validator")
		}
		w.Header().Set("ETag", `"uncommitted"`)
		_, _ = fmt.Fprint(w, persistenceFeed(strings.Repeat("g", 40*1024)))
	}))
	defer server.Close()
	for range 2 {
		if _, err := manager.AddFeed(server.URL); err == nil {
			t.Fatal("oversized article key unexpectedly saved")
		}
		feeds, err := store.GetAllFeeds()
		if err != nil || len(feeds) != 0 {
			t.Fatalf("failed subscription left metadata behind: %#v, %v", feeds, err)
		}
	}
	if requests.Load() != 2 {
		t.Fatal("failed creation prevented retry")
	}
}

func TestFailedRefreshKeepsPreviouslyCommittedValidatorsAndArticles(t *testing.T) {
	manager, store := persistenceManager(t)
	var broken atomic.Bool
	var badRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guid, etag := "stable", `"saved"`
		if broken.Load() {
			badRequests.Add(1)
			if r.Header.Get("If-None-Match") != `"saved"` {
				t.Errorf("refresh used uncommitted validator: %q", r.Header.Get("If-None-Match"))
			}
			guid, etag = strings.Repeat("g", 40*1024), `"not-saved"`
		}
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = fmt.Fprint(w, persistenceFeed(guid))
	}))
	defer server.Close()
	value, err := manager.AddFeed(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	articles, err := store.GetArticles(value.ID, 0)
	if err != nil || len(articles) != 1 {
		t.Fatalf("saved articles = %#v, %v", articles, err)
	}
	if markErr := store.MarkArticleStarred(articles[0].ID, true); markErr != nil {
		t.Fatal(markErr)
	}
	before, err := store.GetFeed(value.ID)
	if err != nil {
		t.Fatal(err)
	}
	broken.Store(true)
	for range 2 {
		if refreshErr := manager.RefreshFeed(value.ID); refreshErr == nil {
			t.Fatal("invalid refreshed article unexpectedly committed")
		}
		got, getErr := store.GetFeed(value.ID)
		if getErr != nil || got.ETag != before.ETag || !got.LastFetched.Equal(before.LastFetched) {
			t.Fatalf("failed refresh advanced metadata: %#v, %v", got, getErr)
		}
	}
	if badRequests.Load() != 2 {
		t.Fatal("failed refresh was not retried")
	}
	got, err := store.GetArticle(articles[0].ID)
	if err != nil || !got.Starred {
		t.Fatal("failed refresh changed saved article")
	}
}

type committedFeedListener struct{ last *storage.Feed }

func (l *committedFeedListener) OnDataUpdated(value *storage.Feed, _ []*storage.Article) {
	if value != nil {
		snapshot := *value
		l.last = &snapshot
	}
}

func TestRefreshNotificationUsesCommittedUserMetadata(t *testing.T) {
	manager, store := persistenceManager(t)
	listener := &committedFeedListener{}
	manager.RegisterDataListener(listener)
	var refreshing atomic.Bool
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if refreshing.Load() {
			close(started)
			<-release
		}
		_, _ = fmt.Fprint(w, persistenceFeed("stable"))
	}))
	defer server.Close()
	value, err := manager.AddFeed(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	refreshing.Store(true)
	done := make(chan error, 1)
	go func() { done <- manager.RefreshFeed(value.ID) }()
	<-started
	if _, renameErr := store.RenameFeed(value.ID, "My title"); renameErr != nil {
		close(release)
		t.Fatal(renameErr)
	}
	if _, deskErr := store.SetFeedDesk(value.ID, "technology"); deskErr != nil {
		close(release)
		t.Fatal(deskErr)
	}
	close(release)
	if refreshErr := <-done; refreshErr != nil {
		t.Fatal(refreshErr)
	}
	if listener.last == nil || listener.last.Title != "My title" || listener.last.DeskID != "technology" {
		t.Fatalf("published stale metadata: %#v", listener.last)
	}
}

func TestDuplicateSubscriptionPreservesUserState(t *testing.T) {
	manager, store := persistenceManager(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, persistenceFeed("stable"))
	}))
	defer server.Close()
	first, err := manager.AddFeed(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	articles, err := store.GetArticles(first.ID, 0)
	if err != nil || len(articles) != 1 {
		t.Fatal("subscription article missing")
	}
	if _, setErr := store.SetFeedDesk(first.ID, "technology"); setErr != nil {
		t.Fatal(setErr)
	}
	if markErr := store.MarkArticleStarred(articles[0].ID, true); markErr != nil {
		t.Fatal(markErr)
	}
	if markErr := store.MarkArticleRead(articles[0].ID, false); markErr != nil {
		t.Fatal(markErr)
	}
	second, err := manager.AddFeedContext(context.Background(), server.URL)
	if err != nil || second.ID != first.ID || second.DeskID != "technology" || !second.SubscribedAt.Equal(first.SubscribedAt) {
		t.Fatalf("duplicate changed feed: %#v, %v", second, err)
	}
	got, err := store.GetArticle(articles[0].ID)
	if err != nil || !got.Starred || got.Read {
		t.Fatal("duplicate changed reader state")
	}
	if requests.Load() != 1 {
		t.Fatal("duplicate subscription was unnecessarily fetched again")
	}
}
