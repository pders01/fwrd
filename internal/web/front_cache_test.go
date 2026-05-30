package web

import (
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

// TestFrontView_CachesUntilStoreChanges verifies frontView memoizes the topic
// model and rebuilds only after a store mutation bumps WriteGen.
func TestFrontView_CachesUntilStoreChanges(t *testing.T) {
	srv, store := newTestServer(t)

	if err := store.SaveFeed(&storage.Feed{ID: "f1", URL: "http://example.com/feed", Title: "Example"}); err != nil {
		t.Fatalf("SaveFeed: %v", err)
	}
	if err := store.SaveArticles([]*storage.Article{
		{ID: "f1:a1", FeedID: "f1", Title: "Alpha story", Published: time.Now()},
	}); err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}

	m1, n1 := srv.frontView()
	m2, _ := srv.frontView()
	if m1 != m2 {
		t.Fatalf("expected cached model on second call, got rebuild")
	}
	if n1["f1"] != "Example" {
		t.Fatalf("feed name not resolved: %q", n1["f1"])
	}

	// A new article advances WriteGen and must invalidate the cache.
	if err := store.SaveArticles([]*storage.Article{
		{ID: "f1:a2", FeedID: "f1", Title: "Beta story", Published: time.Now()},
	}); err != nil {
		t.Fatalf("SaveArticles 2: %v", err)
	}
	m3, _ := srv.frontView()
	if m3 == m1 {
		t.Fatalf("expected rebuild after store change, got cached model")
	}

	// Stable again with no further writes.
	m4, _ := srv.frontView()
	if m4 != m3 {
		t.Fatalf("expected cached model after rebuild, got another rebuild")
	}
}
