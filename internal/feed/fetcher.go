package feed

import (
	"fmt"
	"net/http"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

type Fetcher struct {
	client    *http.Client
	config    *config.FeedConfig
	userAgent string
}

func NewFetcher(cfg *config.Config) *Fetcher {
	return &Fetcher{
		client: &http.Client{
			Timeout: cfg.Feed.HTTPTimeout,
		},
		config:    &cfg.Feed,
		userAgent: cfg.Feed.UserAgent,
	}
}

func (f *Fetcher) Fetch(feed *storage.Feed) (*http.Response, bool, error) {
	req, err := http.NewRequest("GET", feed.URL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")

	if feed.ETag != "" {
		req.Header.Set("If-None-Match", feed.ETag)
	}

	if feed.LastModified != "" {
		req.Header.Set("If-Modified-Since", feed.LastModified)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		return nil, false, fmt.Errorf("fetching feed: %w", err)
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
