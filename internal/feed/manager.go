package feed

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/plugins"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/validation"
)

// maxFeedBodySize caps how many bytes the parser will consume from a
// remote response. Real-world feeds are typically well under 10 MB; the
// cap exists to block hostile or accidentally-huge responses from
// driving us OOM.
const maxFeedBodySize int64 = 50 * 1024 * 1024 // 50 MiB

// Manager orchestrates feed fetch/parse/store. All fields are either
// immutable after construction or independently goroutine-safe (bbolt for
// the store, net/http for the fetcher's client). Methods are safe to call
// from multiple goroutines without external synchronisation.
type Manager struct {
	store          *storage.Store
	fetcher        *Fetcher
	parser         *Parser
	config         *config.Config
	urlValidator   *validation.FeedURLValidator
	pluginRegistry *plugins.Registry

	dataListeners []DataListener
	batchScopes   []BatchScope
}

func NewManager(store *storage.Store, cfg *config.Config) *Manager {
	// Use secure validator by default, can be made configurable later
	urlValidator := validation.NewFeedURLValidator()

	// Initialize plugin registry with HTTP timeout from config
	pluginRegistry := plugins.NewRegistry(cfg.Feed.HTTPTimeout)

	return &Manager{
		store:          store,
		fetcher:        NewFetcher(cfg),
		parser:         NewParser(),
		config:         cfg,
		urlValidator:   urlValidator,
		pluginRegistry: pluginRegistry,
	}
}

// SetForceRefresh configures the manager to ignore ETag/Last-Modified headers
func (m *Manager) SetForceRefresh(force bool) {
	if m.fetcher != nil {
		m.fetcher.SetIgnoreCache(force)
	}
}

// PluginRegistry returns the registry plugins are registered against.
// Callers wire scriptable plugin loaders against this registry at
// startup. The returned pointer is the manager's own registry; mutating
// it after AddFeed has begun running is not safe.
func (m *Manager) PluginRegistry() *plugins.Registry {
	return m.pluginRegistry
}

// PluginHTTPClient returns the HTTP client plugins should use for
// outbound requests. Sharing the fetcher's client keeps the User-Agent,
// timeout, and TLS settings consistent between core feed fetches and
// plugin-driven enhancement calls.
func (m *Manager) PluginHTTPClient() *http.Client {
	if m.fetcher == nil {
		return nil
	}
	return m.fetcher.client
}

// SetPermissiveValidation enables permissive URL validation for development/testing
func (m *Manager) SetPermissiveValidation(permissive bool) {
	if permissive {
		m.urlValidator = validation.NewPermissiveFeedURLValidator()
	} else {
		m.urlValidator = validation.NewFeedURLValidator()
	}
}

// RegisterDataListener subscribes l to post-write notifications. Listeners
// must be registered before any goroutines start using the manager;
// registration is not safe to interleave with concurrent operations.
func (m *Manager) RegisterDataListener(l DataListener) {
	if l != nil {
		m.dataListeners = append(m.dataListeners, l)
	}
}

// RegisterBatchScope subscribes s to RefreshAllFeeds bracketing.
func (m *Manager) RegisterBatchScope(s BatchScope) {
	if s != nil {
		m.batchScopes = append(m.batchScopes, s)
	}
}

func (m *Manager) notifyDataUpdated(feed *storage.Feed, articles []*storage.Article) {
	for _, l := range m.dataListeners {
		l.OnDataUpdated(feed, articles)
	}
}

func (m *Manager) beginBatchScopes() {
	for _, s := range m.batchScopes {
		s.BeginBatch()
	}
}

func (m *Manager) commitBatchScopes() {
	for _, s := range m.batchScopes {
		s.CommitBatch()
	}
}

// AddFeed validates the URL, optionally enhances it via plugins, fetches
// and parses the feed, persists the result, and notifies registered
// DataListeners. The returned feed and saved articles are also handed to
// listeners.
func (m *Manager) AddFeed(url string) (*storage.Feed, error) {
	normalizedURL, err := m.urlValidator.ValidateAndNormalize(url)
	if err != nil {
		return nil, fmt.Errorf("invalid feed URL: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), m.config.Feed.HTTPTimeout)
	defer cancel()

	feedInfo, err := m.pluginRegistry.EnhanceFeed(ctx, normalizedURL)
	if err != nil {
		feedInfo = &plugins.FeedInfo{
			OriginalURL: normalizedURL,
			FeedURL:     normalizedURL,
			Metadata:    map[string]string{},
		}
	}

	actualFeedURL := feedInfo.FeedURL
	if actualFeedURL == "" {
		actualFeedURL = normalizedURL
	}

	feed := &storage.Feed{
		ID:        generateFeedID(actualFeedURL),
		URL:       actualFeedURL,
		Title:     feedInfo.Title,
		UpdatedAt: time.Now(),
	}

	resp, updated, err := m.fetcher.Fetch(feed)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}
	if !updated || resp == nil {
		return nil, fmt.Errorf("feed not modified")
	}
	defer resp.Body.Close()

	articles, err := m.parser.Parse(io.LimitReader(resp.Body, maxFeedBodySize), feed.ID)
	if err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}

	if feed.Title == "" && len(articles) > 0 {
		feed.Title = extractFeedTitleFromArticles(articles)
	}

	m.fetcher.UpdateFeedMetadata(feed, resp)

	if err := m.store.SaveFeed(feed); err != nil {
		return nil, fmt.Errorf("saving feed: %w", err)
	}
	if err := m.store.SaveArticles(articles); err != nil {
		return nil, fmt.Errorf("saving articles: %w", err)
	}

	m.notifyDataUpdated(feed, articles)
	return feed, nil
}

// RefreshFeed re-fetches a single feed and notifies listeners on success.
func (m *Manager) RefreshFeed(feedID string) error {
	_, _, err := m.refreshFeedByID(feedID, true)
	return err
}

// refreshFeedByID does the work of RefreshFeed and returns the feed +
// freshly-saved articles so RefreshAllFeeds can dispatch listener
// notifications from a single goroutine. When notify is true,
// notifyDataUpdated runs inline; the multi-feed path passes false and
// notifies later from the result-collection loop.
func (m *Manager) refreshFeedByID(feedID string, notify bool) (*storage.Feed, []*storage.Article, error) {
	feed, err := m.store.GetFeed(feedID)
	if err != nil {
		return nil, nil, fmt.Errorf("getting feed: %w", err)
	}

	if time.Since(feed.LastFetched) < m.config.Feed.RefreshInterval {
		return feed, nil, nil
	}

	resp, updated, err := m.fetcher.Fetch(feed)
	if err != nil {
		return feed, nil, fmt.Errorf("fetching feed: %w", err)
	}

	if !updated || resp == nil {
		feed.LastFetched = time.Now()
		if saveErr := m.store.SaveFeed(feed); saveErr != nil {
			return feed, nil, fmt.Errorf("saving feed metadata: %w", saveErr)
		}
		return feed, nil, nil
	}
	defer resp.Body.Close()

	articles, err := m.parser.Parse(io.LimitReader(resp.Body, maxFeedBodySize), feedID)
	if err != nil {
		return feed, nil, fmt.Errorf("parsing feed: %w", err)
	}

	m.fetcher.UpdateFeedMetadata(feed, resp)
	feed.UpdatedAt = time.Now()

	if err := m.store.SaveFeed(feed); err != nil {
		return feed, nil, fmt.Errorf("saving feed: %w", err)
	}
	if err := m.store.SaveArticles(articles); err != nil {
		return feed, nil, fmt.Errorf("saving articles: %w", err)
	}

	if notify {
		m.notifyDataUpdated(feed, articles)
	}
	return feed, articles, nil
}

// RefreshAllFeeds refreshes every persisted feed in parallel and returns
// a summary the caller can render. Listener notifications and batch
// scope brackets fire from a single goroutine after all worker
// goroutines complete, so listener implementations need not be safe
// for concurrent invocation.
func (m *Manager) RefreshAllFeeds() (RefreshSummary, error) {
	feeds, err := m.store.GetAllFeeds()
	if err != nil {
		return RefreshSummary{}, fmt.Errorf("getting feeds: %w", err)
	}
	if len(feeds) == 0 {
		return RefreshSummary{}, nil
	}

	type result struct {
		feed     *storage.Feed
		articles []*storage.Article
		err      error
	}

	maxConcurrent := m.config.Feed.MaxConcurrentRefreshes
	if maxConcurrent <= 0 {
		maxConcurrent = config.DefaultMaxConcurrentRefreshes
	}
	feedChan := make(chan *storage.Feed, len(feeds))
	resultChan := make(chan result, len(feeds))

	var wg sync.WaitGroup
	workers := min(maxConcurrent, len(feeds))
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for f := range feedChan {
				feed, articles, err := m.refreshFeedByID(f.ID, false)
				resultChan <- result{feed: feed, articles: articles, err: err}
			}
		}()
	}
	for _, f := range feeds {
		feedChan <- f
	}
	close(feedChan)
	wg.Wait()
	close(resultChan)

	m.beginBatchScopes()
	defer m.commitBatchScopes()

	var summary RefreshSummary
	for r := range resultChan {
		if r.err != nil {
			summary.Errors = append(summary.Errors, r.err)
			continue
		}
		if r.articles == nil {
			// Refresh skipped (rate-limited or 304) — no listener event.
			continue
		}
		summary.UpdatedFeeds++
		summary.AddedArticles += len(r.articles)
		m.notifyDataUpdated(r.feed, r.articles)
	}

	return summary, errors.Join(summary.Errors...)
}

func generateFeedID(url string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
}

func extractFeedTitleFromArticles(articles []*storage.Article) string {
	if len(articles) > 0 && articles[0].URL != "" {
		parts := strings.SplitN(articles[0].URL, "/", 4)
		if len(parts) >= 3 {
			return parts[2]
		}
	}
	return "Unknown Feed"
}
