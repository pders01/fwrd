package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

var integrationBaseURL string

func TestMain(m *testing.M) {
	rss, err := os.ReadFile(filepath.Join("..", "fixtures", "sample-rss.xml"))
	if err != nil {
		panic(err)
	}
	atom, err := os.ReadFile(filepath.Join("..", "fixtures", "sample-atom.xml"))
	if err != nil {
		panic(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/feed.rss", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(rss)
	})
	mux.HandleFunc("/feed.atom", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/atom+xml")
		_, _ = w.Write(atom)
	})
	mux.HandleFunc("/cached-feed.rss", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		w.Header().Set("ETag", `"test-etag-123"`)
		w.Header().Set("Last-Modified", "Wed, 01 Jan 2025 00:00:00 GMT")
		_, _ = w.Write(rss)
	})
	mux.HandleFunc("/rate-limited.rss", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too Many Requests", http.StatusTooManyRequests)
	})

	server := httptest.NewServer(mux)
	integrationBaseURL = server.URL
	code := m.Run()
	server.Close()
	os.Exit(code)
}

func integrationURL(path string) string { return integrationBaseURL + path }

func setupTestEnvironment(t *testing.T) (*storage.Store, *feed.Manager, func()) {
	tmpDir, err := os.MkdirTemp("", "integration-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	cfg := config.TestConfig()
	manager := feed.NewManager(store, cfg)
	// Enable permissive validation for testing with localhost URLs
	manager.SetPermissiveValidation(true)

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, manager, cleanup
}

func TestIntegration_FetchRSSFeed(t *testing.T) {
	store, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test fetching RSS feed
	feedURL := integrationURL("/feed.rss")
	feedResult, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add RSS feed: %v", err)
	}

	if feedResult.URL != feedURL {
		t.Errorf("Expected URL %s, got %s", feedURL, feedResult.URL)
	}

	// Check articles were saved
	articles, err := store.GetArticles(feedResult.ID, 10)
	if err != nil {
		t.Fatalf("Failed to get articles: %v", err)
	}

	if len(articles) != 3 {
		t.Errorf("Expected 3 articles, got %d", len(articles))
	}

	// Verify article content
	for _, article := range articles {
		if article.Title == "" {
			t.Error("Article has empty title")
		}
		if article.URL == "" {
			t.Error("Article has empty URL")
		}
	}
}

func TestIntegration_FetchAtomFeed(t *testing.T) {
	store, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test fetching Atom feed
	feedURL := integrationURL("/feed.atom")
	feedResult, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add Atom feed: %v", err)
	}

	articles, err := store.GetArticles(feedResult.ID, 10)
	if err != nil {
		t.Fatalf("Failed to get articles: %v", err)
	}

	if len(articles) != 2 {
		t.Errorf("Expected 2 articles, got %d", len(articles))
	}
}

func TestIntegration_CachingHeaders(t *testing.T) {
	_, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// First fetch - should get content with ETag
	feedURL := integrationURL("/cached-feed.rss")
	feed1, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add cached feed: %v", err)
	}

	if feed1.ETag != "\"test-etag-123\"" {
		t.Errorf("Expected ETag \"test-etag-123\", got %s", feed1.ETag)
	}

	if feed1.LastModified == "" {
		t.Errorf("Expected Last-Modified header to be set, but it was empty")
	}

	// Second fetch - should get 304 Not Modified
	err = manager.RefreshFeed(feed1.ID)
	if err != nil {
		t.Errorf("Refresh should handle 304 response: %v", err)
	}
}

func TestIntegration_RateLimiting(t *testing.T) {
	_, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	// Test rate limited endpoint
	feedURL := integrationURL("/rate-limited.rss")
	_, err := manager.AddFeed(feedURL)

	if err == nil {
		t.Error("Expected error for rate limited feed, got nil")
	}
}

func TestIntegration_MediaExtraction(t *testing.T) {
	store, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	feedURL := integrationURL("/feed.rss")
	feedResult, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add feed: %v", err)
	}

	articles, err := store.GetArticles(feedResult.ID, 10)
	if err != nil {
		t.Fatalf("Failed to get articles: %v", err)
	}

	// Check media URLs were extracted
	var foundImage bool
	for _, article := range articles {
		for _, url := range article.MediaURLs {
			if strings.HasSuffix(url, "/image1.jpg") {
				foundImage = true
				break
			}
		}
		if foundImage {
			break
		}
	}

	if !foundImage {
		t.Error("Expected to find image URL in articles")
	}
}
