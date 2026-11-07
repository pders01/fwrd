package feed

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/pders01/fwrd/internal/audit"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/validation"
)

type Fetcher struct {
	client      *http.Client
	config      *config.FeedConfig
	userAgent   string
	ignoreCache bool
	policy      *validation.HTTPPolicy
}

func NewFetcher(cfg *config.Config) *Fetcher {
	policy := new(validation.HTTPPolicy)
	return &Fetcher{
		client:      policy.NewHTTPClient(cfg.Feed.HTTPTimeout),
		policy:      policy,
		config:      &cfg.Feed,
		userAgent:   cfg.Feed.UserAgent,
		ignoreCache: false,
	}
}

// SetPermissiveValidation explicitly allows private/local HTTP destinations.
// It updates the existing shared client, including plugin calls and redirects.
// Production callers must leave the secure default (false) in place.
func (f *Fetcher) SetPermissiveValidation(allow bool) {
	f.policy.SetPermissive(allow)
}

// SetIgnoreCache sets whether to ignore ETag/Last-Modified headers
func (f *Fetcher) SetIgnoreCache(ignore bool) {
	f.ignoreCache = ignore
}

func (f *Fetcher) Fetch(feed *storage.Feed) (*http.Response, bool, error) {
	return f.FetchContext(context.Background(), feed)
}

// FetchContext fetches a feed and cancels the HTTP request when ctx ends.
func (f *Fetcher) FetchContext(ctx context.Context, feed *storage.Feed) (*http.Response, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, feed.URL, http.NoBody)
	if err != nil {
		return nil, false, fmt.Errorf("creating request: %w", audit.SafeHTTPError(err))
	}
	// Tag the request so the audit RoundTripper (if installed) attributes it
	// to feed fetching rather than a plugin call.
	req = req.WithContext(audit.WithSource(req.Context(), "feed"))

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")

	// Only set cache headers if not ignoring cache
	if !f.ignoreCache {
		if feed.ETag != "" {
			req.Header.Set("If-None-Match", feed.ETag)
		}

		if feed.LastModified != "" {
			req.Header.Set("If-Modified-Since", feed.LastModified)
		}
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("fetching feed: %w", audit.SafeHTTPError(err))
	}

	if resp.StatusCode == http.StatusNotModified {
		resp.Body.Close()
		return nil, false, nil
	}

	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, false, fmt.Errorf("HTTP error: %d", resp.StatusCode)
	}

	return resp, true, nil
}

func (f *Fetcher) UpdateFeedMetadata(feed *storage.Feed, resp *http.Response) {
	if etag := resp.Header.Get("ETag"); etag != "" {
		feed.ETag = etag
	}

	if lastMod := resp.Header.Get("Last-Modified"); lastMod != "" {
		feed.LastModified = lastMod
	}

	feed.LastFetched = time.Now()
}

func (f *Fetcher) GetRetryAfter(resp *http.Response) time.Duration {
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		if seconds, err := time.ParseDuration(retryAfter + "s"); err == nil {
			return seconds
		}
	}
	return f.config.DefaultRetryAfter
}
