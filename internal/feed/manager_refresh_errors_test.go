package feed

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const refreshErrorTestRSS = `<rss version="2.0"><channel><title>Healthy</title><item><title>Committed</title><guid>refresh-error-test-item</guid></item></channel></rss>`

func TestRefreshAllFeedsErrorAttribution(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/healthy" {
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, refreshErrorTestRSS)
			return
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer server.Close()

	store, err := storage.NewStore(storage.MemoryPath)
	require.NoError(t, err)
	defer store.Close()
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 0
	cfg.Feed.MaxConcurrentRefreshes = 4
	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true) // localhost only, never external HTTP
	listener := &recordingListener{}
	manager.RegisterDataListener(listener)
	manager.RegisterBatchScope(listener)

	previousFetch := time.Now().Add(-time.Hour).Truncate(time.Second)
	healthy := &storage.Feed{
		ID: "healthy", URL: server.URL + "/healthy", LastFetched: previousFetch,
		LastError: "old failure", LastErrorAt: previousFetch,
	}
	require.NoError(t, store.SaveFeed(healthy))

	failed := make([]*storage.Feed, 0, 3)
	for i, title := range []string{"", " \t\n", "\x1b[31m" + strings.Repeat("untrusted-title", 4096)} {
		u, parseErr := url.Parse(server.URL)
		require.NoError(t, parseErr)
		u.User = url.UserPassword("secret-user", "secret-password")
		u.Path = fmt.Sprintf("/sensitive-path-%d", i)
		u.RawQuery = "token=secret-query"
		u.Fragment = "secret-fragment"
		f := &storage.Feed{
			ID:  fmt.Sprintf("arbitrary-id-%d-\x1b[2J\n", i) + strings.Repeat("x", 2048),
			URL: u.String(), Title: title, LastFetched: previousFetch,
		}
		require.NoError(t, store.SaveFeed(f))
		failed = append(failed, f)
	}

	summary, aggregate := manager.RefreshAllFeedsContext(context.Background())
	require.Error(t, aggregate)
	require.Len(t, summary.Errors, 3)
	assert.Equal(t, 1, summary.UpdatedFeeds)
	assert.Equal(t, 1, summary.AddedArticles)
	assert.Equal(t, int32(4), requests.Load(), "no retries")

	labels := make(map[string]bool)
	for _, f := range failed {
		label := refreshFeedIdentity(f)
		assert.False(t, labels[label], "different subscriptions must have different labels")
		labels[label] = true
		matches := 0
		for _, refreshErr := range summary.Errors {
			if strings.HasPrefix(refreshErr.Error(), label+": ") {
				matches++
				assert.Equal(t, label+": fetching feed: HTTP error: 504", refreshErr.Error())
				assert.EqualError(t, errors.Unwrap(refreshErr), "fetching feed: HTTP error: 504")
				assert.ErrorIs(t, aggregate, refreshErr)
			}
		}
		assert.Equal(t, 1, matches, "each failed feed must be attributable")
		persisted, getErr := store.GetFeed(f.ID)
		require.NoError(t, getErr)
		assert.Equal(t, "HTTP error: 504", persisted.LastError, "persistence must remain unwrapped")
		assert.False(t, persisted.LastErrorAt.IsZero())
		assert.Equal(t, previousFetch, persisted.LastFetched)
		assert.Contains(t, label, "127.0.0.1")
	}
	for _, secret := range []string{
		"secret-user", "secret-password", "sensitive-path", "secret-query", "secret-fragment",
		"token=", "arbitrary-id", "untrusted-title", "\x1b", "\t",
	} {
		assert.NotContains(t, aggregate.Error(), secret)
	}
	assert.Equal(t, 2, strings.Count(aggregate.Error(), "\n"), "only errors.Join separators, not injected lines")

	persisted, err := store.GetFeed(healthy.ID)
	require.NoError(t, err)
	assert.Empty(t, persisted.LastError)
	assert.True(t, persisted.LastErrorAt.IsZero())
	assert.True(t, persisted.LastFetched.After(previousFetch))
	articles, err := store.GetArticles(healthy.ID, 10)
	require.NoError(t, err)
	require.Len(t, articles, 1)
	assert.Equal(t, "Committed", articles[0].Title)
	updates, articleCount, begins, commits := listener.snapshot()
	assert.Equal(t, 1, updates)
	assert.Equal(t, 1, articleCount)
	assert.Equal(t, 1, begins)
	assert.Equal(t, 1, commits)
	assert.Equal(t, []string{healthy.ID}, listener.updates)
}

func TestRefreshAllFeedsErrorNilFeedIdentity(t *testing.T) {
	store, err := storage.NewStore(storage.MemoryPath)
	require.NoError(t, err)
	defer store.Close()

	// One worker visits A before B. Removing B during A's fetch makes B's
	// refresh fail at GetFeed, which returns a nil refreshed feed.
	deleteResult := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deleteResult <- store.DeleteFeed("removed-feed")
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, refreshErrorTestRSS)
	}))
	defer server.Close()
	first := &storage.Feed{ID: "first", Title: "A", URL: server.URL}
	removed := &storage.Feed{ID: "removed-feed", Title: "B", URL: server.URL + "/private-path?token=private-query#private-fragment"}
	require.NoError(t, store.SaveFeed(first))
	require.NoError(t, store.SaveFeed(removed))
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 0
	cfg.Feed.MaxConcurrentRefreshes = 1
	manager := NewManager(store, cfg)
	manager.SetPermissiveValidation(true)

	summary, aggregate := manager.RefreshAllFeedsContext(context.Background())
	require.NoError(t, <-deleteResult)
	require.Error(t, aggregate)
	require.Len(t, summary.Errors, 1)
	assert.Equal(t, 1, summary.UpdatedFeeds)
	assert.Equal(t, refreshFeedIdentity(removed)+": getting feed: feed not found", summary.Errors[0].Error())
	assert.NotContains(t, aggregate.Error(), refreshFeedIdentity(first))
	assert.NotContains(t, aggregate.Error(), "private-")
}

type refreshErrorTransport func(*http.Request) (*http.Response, error)

func (f refreshErrorTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRefreshAllFeedsErrorPreservesIdentity(t *testing.T) {
	store, err := storage.NewStore(storage.MemoryPath)
	require.NoError(t, err)
	defer store.Close()
	f := &storage.Feed{ID: "sentinel-feed", URL: "http://127.0.0.1/private?token=secret#fragment"}
	require.NoError(t, store.SaveFeed(f))
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 0
	manager := NewManager(store, cfg)
	sentinel := errors.New("transport sentinel")
	manager.fetcher.client.Transport = refreshErrorTransport(func(*http.Request) (*http.Response, error) {
		return nil, sentinel // deterministic failure, no network access
	})

	summary, aggregate := manager.RefreshAllFeedsContext(context.Background())
	require.Error(t, aggregate)
	require.Len(t, summary.Errors, 1)
	assert.ErrorIs(t, summary.Errors[0], sentinel)
	assert.ErrorIs(t, aggregate, sentinel)
	assert.Contains(t, summary.Errors[0].Error(), refreshFeedIdentity(f))
	assert.NotContains(t, aggregate.Error(), "/private")
	assert.NotContains(t, aggregate.Error(), "token=secret")
}

func TestRefreshFeedIdentitySafeFallback(t *testing.T) {
	for _, rawURL := range []string{
		"https://user:password@example.org/private?token=secret#fragment",
		"https://[::1]/private?token=secret",
		"https://bad\n.example/private",
		"https://bad\u202e.example/private",
		"https://" + strings.Repeat("x", 4096) + "/private",
		"not a URL",
		"",
	} {
		for _, id := range []string{"", " \t\n", "\x1b[2J" + strings.Repeat("id", 4096)} {
			f := &storage.Feed{ID: id, URL: rawURL, Title: "\x1b[31m" + strings.Repeat("title", 4096)}
			label := refreshFeedIdentity(f)
			assert.Equal(t, label, refreshFeedIdentity(f), "stable across calls")
			assert.LessOrEqual(t, len(label), 277)
			for _, r := range label {
				assert.True(t, r >= ' ' && r <= '~', "non-terminal-safe label: %q", label)
			}
			for _, secret := range []string{"user", "password", "private", "token", "secret", "fragment", "title", "\x1b"} {
				assert.NotContains(t, label, secret)
			}
			f.Title = ""
			assert.Equal(t, label, refreshFeedIdentity(f), "title must not affect stable identity")
		}
	}
	standardID := generateFeedID("https://example.org/feed")
	assert.Equal(t, "feed "+standardID[:16]+" (example.org)", refreshFeedIdentity(&storage.Feed{
		ID: standardID, URL: "https://user:password@example.org/private?token=secret#fragment",
	}))
	// Even a URL shaped like a generated ID must be hashed when it is the
	// missing-ID fallback, never printed as a possibly sensitive relative path.
	privatePath := strings.Repeat("abcdef01", 8)
	assert.NotContains(t, refreshFeedIdentity(&storage.Feed{URL: privatePath}), privatePath[:16])
	a := refreshFeedIdentity(&storage.Feed{URL: "https://example.org/one?secret=1"})
	b := refreshFeedIdentity(&storage.Feed{URL: "https://example.org/two?secret=2"})
	assert.NotEqual(t, a, b, "missing IDs must still distinguish subscriptions on one host")
}
