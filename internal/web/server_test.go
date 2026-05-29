package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

	srv, err := NewServer(store, nil, nil, &config.Config{})
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

func postForm(t *testing.T, h http.Handler, path string, form url.Values, sameOrigin bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if sameOrigin {
		req.Header.Set("Origin", "http://"+req.Host)
	} else {
		req.Header.Set("Origin", "http://evil.example")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestToggleRead(t *testing.T) {
	srv, store := newTestServer(t)
	_, articleID := seed(t, store)
	h := srv.Handler()

	rec := postForm(t, h, "/read", url.Values{
		"id":     {articleID},
		"read":   {"1"},
		"return": {"/article?id=" + articleID},
	}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	got, err := store.GetArticle(articleID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if !got.Read {
		t.Error("article should be marked read")
	}
}

func TestDeleteFeed(t *testing.T) {
	srv, store := newTestServer(t)
	feedID, _ := seed(t, store)
	h := srv.Handler()

	rec := postForm(t, h, "/feeds/"+feedID+"/delete", url.Values{}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	if _, err := store.GetFeed(feedID); err == nil {
		t.Error("feed should be deleted")
	}
}

func TestCrossOriginRejected(t *testing.T) {
	srv, store := newTestServer(t)
	_, articleID := seed(t, store)
	h := srv.Handler()

	rec := postForm(t, h, "/read", url.Values{"id": {articleID}, "read": {"1"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for cross-origin POST", rec.Code)
	}
}

func TestFeedManagementDisabled(t *testing.T) {
	srv, _ := newTestServer(t) // nil manager
	h := srv.Handler()

	rec := postForm(t, h, "/feeds", url.Values{"url": {"http://example.com/f"}}, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 when manager is nil", rec.Code)
	}
}

func TestFeedPagination(t *testing.T) {
	srv, store := newTestServer(t)
	feedID := "pager"
	if err := store.SaveFeed(&storage.Feed{ID: feedID, URL: "http://example.com/f"}); err != nil {
		t.Fatalf("SaveFeed: %v", err)
	}
	arts := make([]*storage.Article, 0, articlesPerPage+1)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range articlesPerPage + 1 {
		arts = append(arts, &storage.Article{
			ID:        feedID + ":" + strings.Repeat("x", i+1),
			FeedID:    feedID,
			Title:     "A",
			Published: base.Add(time.Duration(i) * time.Minute),
		})
	}
	if err := store.SaveArticles(arts); err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds/"+feedID, http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cursor=") {
		t.Error("expected a pagination link when articles exceed one page")
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
