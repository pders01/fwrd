package feed

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestFetcher_Fetch(t *testing.T) {
	tests := []struct {
		name           string
		feed           *storage.Feed
		serverResponse func(w http.ResponseWriter, r *http.Request)
		expectUpdated  bool
		expectError    bool
	}{
		{
			name: "successful fetch with new content",
			feed: &storage.Feed{
				ID:  "test1",
				URL: "",
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				expectedUserAgent := "fwrd-test/1.0"
				if r.Header.Get("User-Agent") != expectedUserAgent {
					t.Errorf("expected User-Agent %s, got %s", expectedUserAgent, r.Header.Get("User-Agent"))
				}
				w.Header().Set("ETag", "\"123\"")
				w.Header().Set("Last-Modified", "Wed, 01 Jan 2025 00:00:00 GMT")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("<rss></rss>"))
			},
			expectUpdated: true,
			expectError:   false,
		},
		{
			name: "not modified response with ETag",
			feed: &storage.Feed{
				ID:   "test2",
				URL:  "",
				ETag: "\"123\"",
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("If-None-Match") != "\"123\"" {
					t.Errorf("expected If-None-Match \"123\", got %s", r.Header.Get("If-None-Match"))
				}
				w.WriteHeader(http.StatusNotModified)
			},
			expectUpdated: false,
			expectError:   false,
		},
		{
			name: "not modified response with Last-Modified",
			feed: &storage.Feed{
				ID:           "test3",
				URL:          "",
				LastModified: "Wed, 01 Jan 2025 00:00:00 GMT",
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("If-Modified-Since") != "Wed, 01 Jan 2025 00:00:00 GMT" {
					t.Errorf("expected If-Modified-Since header")
				}
				w.WriteHeader(http.StatusNotModified)
			},
			expectUpdated: false,
			expectError:   false,
		},
		{
			name: "server error",
			feed: &storage.Feed{
				ID:  "test4",
				URL: "",
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusInternalServerError)
			},
			expectUpdated: false,
			expectError:   true,
		},
		{
			name: "rate limit with retry-after",
			feed: &storage.Feed{
				ID:  "test5",
				URL: "",
			},
			serverResponse: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
			},
			expectUpdated: false,
			expectError:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tt.serverResponse))
			defer server.Close()

			tt.feed.URL = server.URL
			fetcher := NewFetcher()

			resp, updated, err := fetcher.Fetch(tt.feed)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if updated != tt.expectUpdated {
				t.Errorf("expected updated=%v, got %v", tt.expectUpdated, updated)
			}
			if resp != nil {
				resp.Body.Close()
			}
		})
	}
}

func TestFetcher_UpdateFeedMetadata(t *testing.T) {
	fetcher := NewFetcher()
	feed := &storage.Feed{
		ID:  "test",
		URL: "http://example.com",
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", "\"new-etag\"")
		w.Header().Set("Last-Modified", "Thu, 02 Jan 2025 00:00:00 GMT")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	resp, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	fetcher.UpdateFeedMetadata(feed, resp)

	if feed.ETag != "\"new-etag\"" {
		t.Errorf("expected ETag \"new-etag\", got %s", feed.ETag)
	}
	if feed.LastModified != "Thu, 02 Jan 2025 00:00:00 GMT" {
		t.Errorf("expected LastModified Thu, 02 Jan 2025 00:00:00 GMT, got %s", feed.LastModified)
	}
	if time.Since(feed.LastFetched) > time.Second {
		t.Error("LastFetched not updated")
	}
}

func TestFetcher_GetRetryAfter(t *testing.T) {
	fetcher := NewFetcher()

	tests := []struct {
		name             string
		retryAfter       string
		expectedDuration time.Duration
	}{
		{
			name:             "valid retry-after in seconds",
			retryAfter:       "120",
			expectedDuration: 120 * time.Second,
		},
		{
			name:             "invalid retry-after",
			retryAfter:       "invalid",
			expectedDuration: 15 * time.Minute,
		},
		{
			name:             "no retry-after header",
			retryAfter:       "",
			expectedDuration: 15 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(http.StatusTooManyRequests)
			}))
			defer server.Close()

			resp, err := http.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			duration := fetcher.GetRetryAfter(resp)
			if duration != tt.expectedDuration {
				t.Errorf("expected duration %v, got %v", tt.expectedDuration, duration)
			}
		})
	}
}
