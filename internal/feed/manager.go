package feed

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"strings"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/audit"
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

// Manager orchestrates feed fetch/parse/store. Configuration and listeners
// must be wired before use. Application-level mutations and listener batches
// must be serialized by the control coordinator; refresh workers may fetch
// different feeds concurrently within that operation.
type Manager struct {
	store          *storage.Store
	fetcher        *Fetcher
	parser         *Parser
	config         *config.Config
	urlValidator   *validation.FeedURLValidator
	pluginRegistry *plugins.Registry
	forceRefresh   bool

	dataListeners   []DataListener
	deleteListeners []DeleteListener
	batchScopes     []BatchScope
}

func NewManager(store *storage.Store, cfg *config.Config) *Manager {
	// Use secure validator by default, can be made configurable later
	urlValidator := validation.NewFeedURLValidator()

	// Initialize plugin registry with HTTP timeout from config
	pluginRegistry := plugins.NewRegistry(cfg.Feed.HTTPTimeout)

	manager := &Manager{
		store:          store,
		fetcher:        NewFetcher(cfg),
		parser:         NewParser(),
		config:         cfg,
		urlValidator:   urlValidator,
		pluginRegistry: pluginRegistry,
	}
	manager.pluginRegistry.SetHTTPClient(manager.fetcher.client)
	if cfg.Feed.AllowPrivateNetworks {
		manager.SetPermissiveValidation(true)
	}
	return manager
}

// SetForceRefresh configures the manager to ignore ETag/Last-Modified headers
func (m *Manager) SetForceRefresh(force bool) {
	m.forceRefresh = force
	if m.fetcher != nil {
		m.fetcher.SetIgnoreCache(force)
	}
}

// ForceRefresh reports the manager's current cache-bypass default. Mutation
// coordination ensures temporary RPC overrides are not changed concurrently.
func (m *Manager) ForceRefresh() bool { return m.forceRefresh }

// PluginRegistry returns the registry plugins are registered against.
// Callers wire scriptable plugin loaders against this registry at
// startup. The returned pointer is the manager's own registry; mutating
// it after AddFeed has begun running is not safe.
func (m *Manager) PluginRegistry() *plugins.Registry {
	return m.pluginRegistry
}

// UseAuditLogger installs an audit RoundTripper on the shared feed/plugin
// HTTP client so every outbound request — core feed fetches and plugin
// http.get alike — is recorded. A nil logger or absent fetcher is a no-op.
// Call it once at startup, before any fetch runs.
func (m *Manager) UseAuditLogger(l *audit.Logger) {
	if m.fetcher == nil || l == nil {
		return
	}
	base := m.fetcher.client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	m.fetcher.client.Transport = &audit.RoundTripper{Base: base, Log: l}
}

// PluginHTTPClient returns the shared timeout/TLS/destination-policy client
// for plugin requests. The feed User-Agent is set on individual feed requests;
// plugins must supply their own User-Agent header if they want to override
// net/http's default.
func (m *Manager) PluginHTTPClient() *http.Client {
	if m.fetcher == nil {
		return nil
	}
	return m.fetcher.client
}

// SetPermissiveValidation enables permissive URL validation for development/testing
func (m *Manager) SetPermissiveValidation(permissive bool) {
	m.fetcher.SetPermissiveValidation(permissive)
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

// RegisterDeleteListener subscribes l to feed deletion notifications.
func (m *Manager) RegisterDeleteListener(l DeleteListener) {
	if l != nil {
		m.deleteListeners = append(m.deleteListeners, l)
	}
}

// DeleteFeed removes a feed and all its articles, then notifies
// registered delete listeners.
func (m *Manager) DeleteFeed(id string) error {
	if err := m.store.DeleteFeed(id); err != nil {
		return err
	}
	for _, l := range m.deleteListeners {
		l.OnFeedDeleted(id)
	}
	return nil
}

func (m *Manager) notifyDataUpdated(feed *storage.Feed, articles []*storage.Article) {
	for _, l := range m.dataListeners {
		l.OnDataUpdated(feed, articles)
	}
}

// NotifyDataUpdated emits a post-write notification for primitive storage
// operations coordinated outside Manager. Callers must serialize it with the
// corresponding mutation just as Manager's own operations do.
func (m *Manager) NotifyDataUpdated(feed *storage.Feed, articles []*storage.Article) {
	m.notifyDataUpdated(feed, articles)
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
	return m.AddFeedContext(context.Background(), url)
}

// AddFeedContext is AddFeed with caller-controlled cancellation.
func (m *Manager) AddFeedContext(ctx context.Context, url string) (*storage.Feed, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	normalizedURL, err := m.urlValidator.ValidateAndNormalize(url)
	if err != nil {
		return nil, fmt.Errorf("invalid feed URL: %w", err)
	}

	existing, err := m.store.GetAllFeeds()
	if err != nil {
		return nil, fmt.Errorf("get existing subscriptions: %w", err)
	}
	for _, current := range existing {
		if current.MatchesURL(normalizedURL) {
			return current, nil
		}
	}
	pluginCtx, cancel := context.WithTimeout(ctx, m.config.Feed.HTTPTimeout)
	defer cancel()

	feedInfo, err := m.pluginRegistry.EnhanceFeed(pluginCtx, normalizedURL)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
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
	actualFeedURL, err = m.urlValidator.ValidateAndNormalize(actualFeedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid resolved feed URL: %w", err)
	}
	for _, current := range existing {
		if current.MatchesURL(actualFeedURL) || current.ID == generateFeedID(actualFeedURL) {
			return current, nil
		}
	}

	subscribedAt := time.Now()
	feed := &storage.Feed{
		ID:              generateFeedID(actualFeedURL),
		URL:             actualFeedURL,
		SubscribedURL:   normalizedURL,
		SubscribedTitle: feedInfo.Title,
		SubscribedAt:    subscribedAt,
		Title:           feedInfo.Title,
		Description:     feedInfo.Description,
		UpdatedAt:       subscribedAt,
	}

	resp, updated, err := m.fetcher.FetchContext(ctx, feed)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}
	if !updated || resp == nil {
		return nil, fmt.Errorf("feed not modified")
	}
	defer resp.Body.Close()

	parsed, err := m.parser.ParseFeed(io.LimitReader(contextReader{ctx: ctx, reader: resp.Body}, maxFeedBodySize), feed.ID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("parsing feed: %w", err)
	}
	articles := parsed.Articles
	applyParsedIdentity(feed, parsed)
	applyOnboardingState(feed, articles)

	m.fetcher.UpdateFeedMetadata(feed, resp)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}

	articles, err = m.store.CommitFeed(feed, articles, true)
	if errors.Is(err, storage.ErrFeedExists) {
		return m.store.GetFeed(feed.ID)
	}
	if err != nil {
		return nil, fmt.Errorf("saving subscription: %w", err)
	}
	feed, err = m.store.GetFeed(feed.ID)
	if err != nil {
		return nil, fmt.Errorf("read committed subscription: %w", err)
	}

	m.notifyDataUpdated(feed, articles)
	return feed, nil
}

// RefreshFeed re-fetches a single feed and notifies listeners on success.
func (m *Manager) RefreshFeed(feedID string) error {
	return m.RefreshFeedContext(context.Background(), feedID)
}

// RefreshFeedContext is RefreshFeed with caller-controlled cancellation.
func (m *Manager) RefreshFeedContext(ctx context.Context, feedID string) error {
	_, _, err := m.refreshFeedByIDContext(ctx, feedID, true)
	return err
}

// refreshFeedByID does the work of RefreshFeed and returns the feed +
// freshly-saved articles so RefreshAllFeeds can dispatch listener
// notifications from a single goroutine. When notify is true,
// notifyDataUpdated runs inline; the multi-feed path passes false and
// notifies later from the result-collection loop.
func (m *Manager) refreshFeedByIDContext(ctx context.Context, feedID string, notify bool) (*storage.Feed, []*storage.Article, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	feed, err := m.store.GetFeed(feedID)
	if err != nil {
		return nil, nil, fmt.Errorf("getting feed: %w", err)
	}

	if time.Since(feed.LastFetched) < m.config.Feed.RefreshInterval {
		return feed, nil, nil
	}

	resp, updated, err := m.fetcher.FetchContext(ctx, feed)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return feed, nil, ctxErr
		}
		// Persist the failure so /feeds can surface a stale/error badge.
		// Best-effort: a save error here is subordinate to the fetch error.
		recordFeedError(feed, err)
		_, _ = m.store.CommitFeed(feed, nil, false)
		return feed, nil, fmt.Errorf("fetching feed: %w", err)
	}

	if !updated || resp == nil {
		// 304/unchanged is a successful round-trip — clear any prior error.
		feed.LastFetched = time.Now()
		clearFeedError(feed)
		if _, saveErr := m.store.CommitFeed(feed, nil, false); saveErr != nil {
			return feed, nil, fmt.Errorf("saving feed metadata: %w", saveErr)
		}
		return feed, nil, nil
	}
	defer resp.Body.Close()

	parsed, err := m.parser.ParseFeed(io.LimitReader(contextReader{ctx: ctx, reader: resp.Body}, maxFeedBodySize), feedID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return feed, nil, ctxErr
		}
		recordFeedError(feed, err)
		_, _ = m.store.CommitFeed(feed, nil, false)
		return feed, nil, fmt.Errorf("parsing feed: %w", err)
	}
	articles := parsed.Articles
	applyParsedIdentity(feed, parsed)

	m.fetcher.UpdateFeedMetadata(feed, resp)
	feed.UpdatedAt = time.Now()
	clearFeedError(feed)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return feed, nil, ctxErr
	}

	// Validators, fetch timestamps, content and current reader state commit
	// together. A rejected article must not make subsequent fetches return 304
	// for content that was never persisted.
	applyOnboardingState(feed, articles)
	articles, err = m.store.CommitFeed(feed, articles, false)
	if err != nil {
		return feed, nil, fmt.Errorf("saving refreshed feed: %w", err)
	}
	feed, err = m.store.GetFeed(feed.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("read committed refreshed feed: %w", err)
	}

	if notify {
		m.notifyDataUpdated(feed, articles)
	}
	return feed, articles, nil
}

// applyOnboardingState marks pre-subscription history read for feeds created
// under the explicit cutoff policy. A zero cutoff identifies a legacy feed and
// intentionally preserves the old behavior; no existing database is migrated
// or reset implicitly.
func applyOnboardingState(feed *storage.Feed, articles []*storage.Article) {
	if feed == nil || feed.SubscribedAt.IsZero() {
		return
	}
	for _, article := range articles {
		if article == nil {
			continue
		}
		if article.Published.IsZero() || !article.Published.After(feed.SubscribedAt) {
			article.Read = true
		}
	}
}

// RefreshAllFeeds refreshes every persisted feed in parallel and returns
// a summary the caller can render. Listener notifications and batch
// scope brackets fire from a single goroutine after all worker
// goroutines complete, so listener implementations need not be safe
// for concurrent invocation.
func (m *Manager) RefreshAllFeeds() (RefreshSummary, error) {
	return m.RefreshAllFeedsContext(context.Background())
}

// RefreshAllFeedsContext is RefreshAllFeeds with caller-controlled cancellation.
func (m *Manager) RefreshAllFeedsContext(ctx context.Context) (RefreshSummary, error) {
	if err := ctx.Err(); err != nil {
		return RefreshSummary{}, err
	}
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
			for {
				select {
				case <-ctx.Done():
					return
				case f, ok := <-feedChan:
					if !ok {
						return
					}
					feed, articles, refreshErr := m.refreshFeedByIDContext(ctx, f.ID, false)
					if refreshErr != nil {
						// Use the original subscription: refresh can return a nil feed.
						refreshErr = fmt.Errorf("%s: %w", refreshFeedIdentity(f), refreshErr)
					}
					resultChan <- result{feed: feed, articles: articles, err: refreshErr}
				}
			}
		}()
	}
feedLoop:
	for _, f := range feeds {
		select {
		case feedChan <- f:
		case <-ctx.Done():
			break feedLoop
		}
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

	return summary, errors.Join(append(summary.Errors, ctx.Err())...)
}

// refreshFeedIdentity is a bounded, terminal-safe label, not a display title.
// Keep the prefix of generated IDs so it can be matched to the stored ID;
// fingerprint arbitrary IDs rather than trusting their contents. For legacy
// missing IDs, fingerprint the URL without ever displaying its sensitive parts.
func refreshFeedIdentity(feed *storage.Feed) string {
	id := feed.ID
	if strings.TrimSpace(id) == "" {
		id = generateFeedID(feed.URL)
	}
	if len(id) != 64 || strings.IndexFunc(id, func(r rune) bool {
		return (r < 'a' || r > 'f') && (r < '0' || r > '9')
	}) != -1 {
		id = generateFeedID(id)
	}
	fingerprint := id[:16]
	host := "unknown-host"
	if parsed, err := neturl.Parse(feed.URL); err == nil {
		candidate := strings.ToLower(parsed.Hostname())
		if candidate != "" && len(candidate) <= 253 && strings.IndexFunc(candidate, func(r rune) bool {
			return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-' && r != ':'
		}) == -1 {
			host = candidate
		}
	}
	return fmt.Sprintf("feed %s (%s)", fingerprint, host)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// recordFeedError stamps a failed refresh onto the feed. LastFetched is left
// untouched so it keeps pointing at the last *successful* fetch.
func recordFeedError(feed *storage.Feed, err error) {
	feed.LastError = err.Error()
	feed.LastErrorAt = time.Now()
}

// clearFeedError wipes any prior failure after a successful refresh.
func clearFeedError(feed *storage.Feed) {
	feed.LastError = ""
	feed.LastErrorAt = time.Time{}
}

func generateFeedID(url string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(url)))
}

func applyParsedIdentity(feed *storage.Feed, parsed *ParsedFeed) {
	if feed == nil || parsed == nil {
		return
	}
	switch {
	case feed.TitleOverride != "":
		feed.Title = feed.TitleOverride
	case feed.SubscribedTitle != "":
		feed.Title = feed.SubscribedTitle
	case strings.TrimSpace(parsed.Title) != "":
		feed.Title = strings.TrimSpace(parsed.Title)
	}
	if feed.Title == "" {
		feed.Title = feedTitleFromURL(feed.SubscriptionURL())
	}
	if strings.TrimSpace(parsed.Description) != "" && feed.Description == "" {
		feed.Description = strings.TrimSpace(parsed.Description)
	}
}

func feedTitleFromURL(raw string) string {
	if parsed, err := neturl.Parse(raw); err == nil && parsed.Hostname() != "" {
		return strings.TrimPrefix(parsed.Hostname(), "www.")
	}
	return "Unknown Feed"
}
