package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func newTestServer(t *testing.T) (*Server, *storage.Store) {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	srv, err := NewServer(store, nil, &config.Config{})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, store
}

func seed(t *testing.T, store *storage.Store) (feedID, articleID string) {
	t.Helper()
	feedID = "feed1"
	if err := store.SaveFeed(&storage.Feed{ID: feedID, URL: "http://example.com/feed", Title: "Example"}); err != nil {
		t.Fatalf("SaveFeed: %v", err)
	}
	// Article ID mimics the real composite scheme: feedID:articleURL.
	articleID = feedID + ":http://example.com/post?id=1"
	art := &storage.Article{
		ID:      articleID,
		FeedID:  feedID,
		Title:   "Hello",
		Content: `<p>safe</p><script>alert(1)</script>`,
		URL:     "http://example.com/post",
	}
	if err := store.SaveArticles([]*storage.Article{art}); err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}
	return feedID, articleID
}

func TestIndexAndFeed(t *testing.T) {
	srv, store := newTestServer(t)
	feedID, _ := seed(t, store)
	h := srv.Handler()

	for _, path := range []string{"/", "/feeds/" + feedID} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "Example") {
			t.Errorf("%s: body missing feed title", path)
		}
	}
}

func TestArticleSanitizesAndRoutesByQuery(t *testing.T) {
	srv, store := newTestServer(t)
	_, articleID := seed(t, store)
	h := srv.Handler()

	target := "/article?id=" + url.QueryEscape(articleID)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "<p>safe</p>") {
		t.Error("expected safe content to survive sanitization")
	}
	if strings.Contains(body, "<script>") {
		t.Error("script tag must be stripped by sanitizer")
	}
}

func TestArticleMissing(t *testing.T) {
	srv, _ := newTestServer(t)
	h := srv.Handler()

	for _, path := range []string{"/article", "/article?id=nope", "/feeds/nope"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, http.NoBody))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestSearchUnavailable(t *testing.T) {
	srv, _ := newTestServer(t) // nil searcher
	h := srv.Handler()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/search?q=x", http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "unavailable") {
		t.Error("expected 'unavailable' notice when searcher is nil")
	}
}
