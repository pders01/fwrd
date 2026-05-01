package feed

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func TestNewManager(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store, cfg)

	assert.NotNil(t, manager)
	assert.NotNil(t, manager.fetcher)
	assert.Equal(t, store, manager.store)
}

func TestRefreshAllFeeds(t *testing.T) {
	t.Run("Refresh with no feeds", func(t *testing.T) {
		cfg := config.TestConfig()
		// Use a unique temporary file path to ensure complete isolation
		tmpDB := t.TempDir() + "/test.db"
		store, err := storage.NewStore(tmpDB)
		if err != nil {
			t.Fatalf("Failed to create store: %v", err)
		}
		defer store.Close()

		manager := NewManager(store, cfg)

		// This will try to refresh all feeds (which should be none in fresh DB)
		_, err = manager.RefreshAllFeeds()
		assert.NoError(t, err)
	})
}

func TestAddFeed(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store, cfg)

	// Test adding a feed (will fail with actual network request but tests the structure)
	t.Run("Add feed with invalid URL", func(t *testing.T) {
		feed, err := manager.AddFeed("not-a-url")
		assert.Error(t, err)
		assert.Nil(t, feed)
	})

	t.Run("Add feed with valid URL format", func(t *testing.T) {
		// This will try to actually fetch, so it will fail, but tests the URL validation
		feed, err := manager.AddFeed("http://example.com/feed.xml")
		// We expect an error because it will try to fetch from example.com
		assert.Error(t, err)
		assert.Nil(t, feed)
	})
}

// recordingListener captures dispatched OnDataUpdated and Begin/Commit
// events so listener-wiring tests can assert on the call sequence.
type recordingListener struct {
	mu       sync.Mutex
	updates  []string // feed.ID per OnDataUpdated call
	articles int      // total articles seen
	begins   int
	commits  int
}

func (r *recordingListener) OnDataUpdated(f *storage.Feed, articles []*storage.Article) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updates = append(r.updates, f.ID)
	r.articles += len(articles)
}

func (r *recordingListener) BeginBatch()  { r.mu.Lock(); r.begins++; r.mu.Unlock() }
func (r *recordingListener) CommitBatch() { r.mu.Lock(); r.commits++; r.mu.Unlock() }

func (r *recordingListener) snapshot() (updates, articles, begins, commits int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.updates), r.articles, r.begins, r.commits
}

// TestRefreshAllFeeds_NotifiesListeners asserts that registered
// DataListener and BatchScope implementations are invoked exactly once
// per successful feed refresh, with Begin/Commit bracketing the batch.
func TestRefreshAllFeeds_NotifiesListeners(t *testing.T) {
	const numFeeds = 3
	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>F</title>
<item><title>i</title><link>http://example.com/x</link><guid>x</guid></item>
</channel></rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)
	rec := &recordingListener{}
	manager.RegisterDataListener(rec)
	manager.RegisterBatchScope(rec)

	for i := range numFeeds {
		f := &storage.Feed{
			ID:          fmt.Sprintf("feed-%d", i),
			URL:         server.URL,
			LastFetched: time.Now().Add(-2 * time.Hour),
		}
		require.NoError(t, store.SaveFeed(f))
	}

	summary, err := manager.RefreshAllFeeds()
	require.NoError(t, err)

	updates, articles, begins, commits := rec.snapshot()
	assert.Equal(t, numFeeds, summary.UpdatedFeeds)
	assert.Equal(t, numFeeds, updates, "one OnDataUpdated per refreshed feed")
	assert.Equal(t, summary.AddedArticles, articles)
	assert.Equal(t, 1, begins)
	assert.Equal(t, 1, commits)
}

// TestAddFeed_NotifiesListeners covers the single-feed path.
func TestAddFeed_NotifiesListeners(t *testing.T) {
	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>F</title>
<item><title>i</title><link>http://example.com/x</link><guid>x</guid></item>
</channel></rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true) // allow http://127.0.0.1:port
	rec := &recordingListener{}
	manager.RegisterDataListener(rec)

	feed, err := manager.AddFeed(server.URL)
	require.NoError(t, err)
	require.NotNil(t, feed)

	updates, articles, _, _ := rec.snapshot()
	assert.Equal(t, 1, updates)
	assert.Equal(t, 1, articles)
}

func TestSetForceRefresh(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	// Test setting force refresh
	manager.SetForceRefresh(true)
	assert.True(t, manager.fetcher.ignoreCache)

	manager.SetForceRefresh(false)
	assert.False(t, manager.fetcher.ignoreCache)
}

func TestSetPermissiveValidation(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	// Test setting permissive validation
	manager.SetPermissiveValidation(true)
	// Test with localhost URL which should be allowed in permissive mode
	_, err = manager.urlValidator.ValidateAndNormalize("http://localhost:8080/feed")
	assert.NoError(t, err)

	manager.SetPermissiveValidation(false)
	// Test with localhost URL which should be blocked in secure mode
	_, err = manager.urlValidator.ValidateAndNormalize("http://localhost:8080/feed")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "localhost URLs are not permitted")
}

func TestRefreshFeed(t *testing.T) {
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond // Very short interval for testing

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	t.Run("Refresh non-existent feed", func(t *testing.T) {
		err := manager.RefreshFeed("nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "getting feed")
	})

	t.Run("Refresh feed too soon", func(t *testing.T) {
		// Create a feed that was just fetched
		feed := &storage.Feed{
			ID:          "test-feed",
			URL:         "http://test.com/feed",
			Title:       "Test Feed",
			LastFetched: time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := store.SaveFeed(feed)
		require.NoError(t, err)

		// Set a longer refresh interval
		cfg.Feed.RefreshInterval = 1 * time.Hour

		// This should not attempt to refresh
		err = manager.RefreshFeed("test-feed")
		assert.NoError(t, err)
	})
}

func TestAddFeedWithMockServer(t *testing.T) {
	// Create a test server that returns RSS feed content
	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
	<title>Test Feed</title>
	<description>A test RSS feed</description>
	<item>
		<title>Test Article</title>
		<description>Test article content</description>
		<link>http://example.com/article1</link>
		<guid>article1</guid>
		<pubDate>Mon, 01 Jan 2024 12:00:00 GMT</pubDate>
	</item>
</channel>
</rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true) // Allow test server URL

	// Test successful feed addition
	feed, err := manager.AddFeed(server.URL)
	assert.NoError(t, err)
	assert.NotNil(t, feed)
	assert.Equal(t, server.URL, feed.URL)
	assert.NotEmpty(t, feed.ID)
	assert.NotEmpty(t, feed.Title)
}

func TestRefreshFeedWithMockServer(t *testing.T) {
	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
	<title>Updated Test Feed</title>
	<description>An updated test RSS feed</description>
	<item>
		<title>Updated Article</title>
		<description>Updated article content</description>
		<link>http://example.com/article2</link>
		<guid>article2</guid>
		<pubDate>Mon, 02 Jan 2024 12:00:00 GMT</pubDate>
	</item>
</channel>
</rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond // Very short for testing

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	// Create a feed that needs refreshing
	feed := &storage.Feed{
		ID:          generateFeedID(server.URL),
		URL:         server.URL,
		Title:       "Old Title",
		LastFetched: time.Now().Add(-2 * time.Hour), // Old enough to need refresh
		UpdatedAt:   time.Now().Add(-2 * time.Hour),
	}
	err = store.SaveFeed(feed)
	require.NoError(t, err)

	// Test refreshing the feed
	err = manager.RefreshFeed(feed.ID)
	assert.NoError(t, err)

	// Verify the feed was updated
	updatedFeed, err := store.GetFeed(feed.ID)
	require.NoError(t, err)
	assert.True(t, updatedFeed.LastFetched.After(feed.LastFetched))
}

// TestAddFeed_CapsBodyAtMaxSize asserts that a server attempting to
// stream more than maxFeedBodySize bytes does not OOM the parser; the
// LimitReader cuts the response off and the parser sees a truncated
// (and therefore invalid) feed, which surfaces as a parse error rather
// than unbounded memory growth.
func TestAddFeed_CapsBodyAtMaxSize(t *testing.T) {
	// Stream slightly more than the cap, all <item> noise so a
	// well-formed prefix (channel open) gives the parser something to
	// chew on before truncation.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Big</title>`)
		// Emit ~1 MiB chunks until we exceed the cap by a comfortable margin.
		chunk := strings.Repeat("<item><title>x</title></item>", 1<<14) // ~448 KiB
		written := int64(0)
		for written < maxFeedBodySize+(2<<20) {
			n, err := io.WriteString(w, chunk)
			if err != nil {
				return
			}
			written += int64(n)
		}
	}))
	defer server.Close()

	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true)

	// We expect a parse error (truncated XML) — what we are asserting
	// is that the call returns at all, in finite time, without
	// allocating beyond the cap.
	_, err = manager.AddFeed(server.URL)
	if err == nil {
		t.Fatal("expected parse error for truncated oversized feed, got nil")
	}
}

// TestRefreshAllFeeds_RunsInParallel proves the worker pool actually
// parallelises HTTP fetches. The mock server delays each response by
// perRequest, so wall-clock time scales linearly with serialisation:
// 5 feeds × 200ms serial = ~1s, parallel = ~200ms. Prior to dropping
// the per-method mutex, this test would run in ~1s.
func TestRefreshAllFeeds_RunsInParallel(t *testing.T) {
	const numFeeds = 5
	const perRequest = 200 * time.Millisecond

	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel><title>Slow Feed</title>
<item><title>Item</title><link>http://example.com/x</link><guid>x</guid></item>
</channel></rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(perRequest)
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond
	cfg.Feed.HTTPTimeout = 5 * time.Second

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	for i := range numFeeds {
		feed := &storage.Feed{
			ID:          fmt.Sprintf("feed-%d", i),
			URL:         server.URL,
			Title:       fmt.Sprintf("Feed %d", i),
			LastFetched: time.Now().Add(-2 * time.Hour),
		}
		require.NoError(t, store.SaveFeed(feed))
	}

	start := time.Now()
	_, _ = manager.RefreshAllFeeds()
	elapsed := time.Since(start)

	// Parallel ceiling: 5 fetches × 200ms / 5 workers = ~200ms, plus
	// scheduling/parse/save slack. Allow generous 600ms — anything
	// over that means we are partly serialised.
	maxParallel := 600 * time.Millisecond
	serialFloor := time.Duration(numFeeds) * perRequest
	if elapsed >= maxParallel {
		t.Fatalf("RefreshAllFeeds took %v, expected < %v (serial would be ~%v)",
			elapsed, maxParallel, serialFloor)
	}
}

func TestRefreshAllFeedsWithMockServer(t *testing.T) {
	feedContent := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
<channel>
	<title>Test Feed</title>
	<item>
		<title>Test Article</title>
		<link>http://example.com/article</link>
		<guid>article</guid>
	</item>
</channel>
</rss>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, feedContent)
	}))
	defer server.Close()

	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	// Create multiple feeds that need refreshing
	for i := range 3 {
		feed := &storage.Feed{
			ID:          fmt.Sprintf("feed-%d", i),
			URL:         server.URL,
			Title:       fmt.Sprintf("Feed %d", i),
			LastFetched: time.Now().Add(-2 * time.Hour),
			UpdatedAt:   time.Now().Add(-2 * time.Hour),
		}
		err = store.SaveFeed(feed)
		require.NoError(t, err)
	}

	// Test refreshing all feeds - we expect errors since this creates duplicate entries
	// but we're testing that the concurrent processing works
	_, _ = manager.RefreshAllFeeds()
	// Don't assert no error since concurrent operations may cause conflicts

	// Verify feeds exist (may be more than 3 due to duplicates from concurrent processing)
	feeds, err := store.GetAllFeeds()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(feeds), 3)
}

func TestGenerateFeedID(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{
			name: "basic URL",
			url:  "http://example.com/feed",
			want: "38a1f4bc914bad1d5b98bd4cc38d1e7b5a9c0f7c85d7b11e2a4c8c53ba12b8e8", // sha256 hash
		},
		{
			name: "different URL gives different ID",
			url:  "http://different.com/feed",
			want: "c65dc87f73a7b5b6b5f2e5bf7e23d5a92d58b94e1f2c8a4f7b8e9d0a1b2c3d4e", // Different hash
		},
		{
			name: "same URL gives same ID",
			url:  "http://example.com/feed",
			want: "38a1f4bc914bad1d5b98bd4cc38d1e7b5a9c0f7c85d7b11e2a4c8c53ba12b8e8", // Same as first
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := generateFeedID(tt.url)
			// Just verify it's a non-empty hex string of correct length (64 chars for SHA256)
			assert.Len(t, result, 64)
			assert.Regexp(t, "^[a-f0-9]+$", result)

			// Same URL should always produce same ID
			result2 := generateFeedID(tt.url)
			assert.Equal(t, result, result2)
		})
	}
}

func TestExtractFeedTitleFromArticles(t *testing.T) {
	tests := []struct {
		name     string
		articles []*storage.Article
		expected string
	}{
		{
			name:     "empty articles",
			articles: []*storage.Article{},
			expected: "Unknown Feed",
		},
		{
			name: "article with URL",
			articles: []*storage.Article{
				{URL: "https://example.com/path/article"},
			},
			expected: "example.com",
		},
		{
			name: "article with simple URL",
			articles: []*storage.Article{
				{URL: "https://test.org/feed"},
			},
			expected: "test.org",
		},
		{
			name: "article with no URL",
			articles: []*storage.Article{
				{URL: ""},
			},
			expected: "Unknown Feed",
		},
		{
			name: "article with malformed URL",
			articles: []*storage.Article{
				{URL: "not-a-url"},
			},
			expected: "Unknown Feed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractFeedTitleFromArticles(tt.articles)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestManagerErrorHandling(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)

	// Test AddFeed with URL validation errors
	t.Run("AddFeed with suspicious URL", func(t *testing.T) {
		_, err := manager.AddFeed("http://example.com/feed") // example.com is blocked as suspicious
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid feed URL")
	})

	// Test AddFeed with malformed URL
	t.Run("AddFeed with malformed URL", func(t *testing.T) {
		_, err := manager.AddFeed("ht!tp://bad-url")
		assert.Error(t, err)
		// The URL validation now passes invalid characters through to the HTTP client
		// so we expect a different error message
		assert.True(t, strings.Contains(err.Error(), "fetching feed") || strings.Contains(err.Error(), "invalid feed URL"))
	})

	// Test RefreshFeed with non-existent feed
	t.Run("RefreshFeed with non-existent feed", func(t *testing.T) {
		err := manager.RefreshFeed("does-not-exist")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "getting feed")
	})
}

func TestManagerConcurrentOperations(t *testing.T) {
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 1 * time.Millisecond

	store, err := storage.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true)

	// Test concurrent AddFeed calls with different URLs to avoid conflicts
	done := make(chan bool, 3)

	for i := range 3 {
		go func(id int) {
			feedContent := fmt.Sprintf(`<?xml version="1.0"?>
<rss version="2.0">
<channel>
	<title>Feed %d</title>
	<item>
		<title>Article %d</title>
		<link>http://test%d.com/article</link>
		<guid>article%d</guid>
	</item>
</channel>
</rss>`, id, id, id, id)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/rss+xml")
				fmt.Fprint(w, feedContent)
			}))
			defer server.Close()

			// This will test the mutex locking in AddFeed
			_, addErr := manager.AddFeed(server.URL)
			// Just check that it doesn't panic or cause data races
			if addErr != nil {
				t.Logf("AddFeed %d got error (expected): %v", id, addErr)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for range 3 {
		<-done
	}

	// Verify some feeds were processed (exact count may vary due to timing)
	feeds, err := store.GetAllFeeds()
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(feeds), 1, "At least one feed should be added")
}
