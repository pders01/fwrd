package integration

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

var caddyCmd *exec.Cmd

func TestMain(m *testing.M) {
	// Start Caddy server
	if err := startCaddy(); err != nil {
		fmt.Printf("Failed to start Caddy: %v\n", err)
		os.Exit(1)
	}

	// Wait for Caddy to start and accept connections
	if err := waitForCaddy("http://127.0.0.1:8080/feed.rss", 30*time.Second); err != nil {
		fmt.Printf("Caddy did not become ready in time: %v\n", err)
		stopCaddy()
		os.Exit(1)
	}

	// Run tests
	code := m.Run()

	// Stop Caddy
	stopCaddy()

	os.Exit(code)
}

func waitForCaddy(url string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("server not ready after %v", timeout)
}

func startCaddy() error {
	caddyPath, err := exec.LookPath("caddy")
	if err != nil {
		return fmt.Errorf("caddy not found in PATH: %w", err)
	}

	caddyfile := filepath.Join("..", "fixtures", "Caddyfile")
	caddyCmd = exec.Command(caddyPath, "run", "--config", caddyfile)
	caddyCmd.Dir = filepath.Join("..", "fixtures")
	caddyCmd.Stdout = os.Stdout
	caddyCmd.Stderr = os.Stderr

	if err := caddyCmd.Start(); err != nil {
		return fmt.Errorf("failed to start caddy: %w", err)
	}

	return nil
}

func stopCaddy() {
	if caddyCmd != nil && caddyCmd.Process != nil {
		caddyCmd.Process.Kill()
		caddyCmd.Wait()
	}
}

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
	feedURL := "http://127.0.0.1:8080/feed.rss"
	feed, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add RSS feed: %v", err)
	}

	if feed.URL != feedURL {
		t.Errorf("Expected URL %s, got %s", feedURL, feed.URL)
	}

	// Check articles were saved
	articles, err := store.GetArticles(feed.ID, 10)
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
	feedURL := "http://127.0.0.1:8080/feed.atom"
	feed, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add Atom feed: %v", err)
	}

	articles, err := store.GetArticles(feed.ID, 10)
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
	feedURL := "http://127.0.0.1:8080/cached-feed.rss"
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
	feedURL := "http://127.0.0.1:8080/rate-limited.rss"
	_, err := manager.AddFeed(feedURL)

	if err == nil {
		t.Error("Expected error for rate limited feed, got nil")
	}
}

func TestIntegration_MediaExtraction(t *testing.T) {
	store, manager, cleanup := setupTestEnvironment(t)
	defer cleanup()

	feedURL := "http://127.0.0.1:8080/feed.rss"
	feed, err := manager.AddFeed(feedURL)
	if err != nil {
		t.Fatalf("Failed to add feed: %v", err)
	}

	articles, err := store.GetArticles(feed.ID, 10)
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
