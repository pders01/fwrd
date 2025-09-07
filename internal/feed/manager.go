package feed

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/validation"
)

type Manager struct {
	store        *storage.Store
	fetcher      *Fetcher
	parser       *Parser
	config       *config.Config
	urlValidator *validation.FeedURLValidator
	mu           sync.RWMutex
}

func NewManager(store *storage.Store, cfg *config.Config) *Manager {
	// Use secure validator by default, can be made configurable later
	urlValidator := validation.NewFeedURLValidator()
	return &Manager{
		store:        store,
		fetcher:      NewFetcher(cfg),
		parser:       NewParser(),
		config:       cfg,
		urlValidator: urlValidator,
	}
}

// SetForceRefresh configures the manager to ignore ETag/Last-Modified headers
func (m *Manager) SetForceRefresh(force bool) {
	if m.fetcher != nil {
		m.fetcher.SetIgnoreCache(force)
	}
}

// SetPermissiveValidation enables permissive URL validation for development/testing
func (m *Manager) SetPermissiveValidation(permissive bool) {
	if permissive {
		m.urlValidator = validation.NewPermissiveFeedURLValidator()
	} else {
		m.urlValidator = validation.NewFeedURLValidator()
	}
}

func (m *Manager) AddFeed(url string) (*storage.Feed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Validate and normalize the URL using comprehensive security checks
	normalizedURL, err := m.urlValidator.ValidateAndNormalize(url)
	if err != nil {
		return nil, fmt.Errorf("invalid feed URL: %w", err)
	}

	feedID := generateFeedID(normalizedURL)

	feed := &storage.Feed{
		ID:        feedID,
		URL:       normalizedURL,
		UpdatedAt: time.Now(),
	}

	resp, updated, err := m.fetcher.Fetch(feed)
	if err != nil {
		return nil, fmt.Errorf("fetching feed: %w", err)
	}

	if !updated && resp == nil {
		return nil, fmt.Errorf("feed not modified")
	}

	if resp == nil {
		return nil, fmt.Errorf("no response received")
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	articles, err := m.parser.Parse(strings.NewReader(string(body)), feedID)
	if err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}

	if len(articles) > 0 {
		feed.Title = extractFeedTitleFromArticles(articles)
	}

	m.fetcher.UpdateFeedMetadata(feed, resp)

	if err := m.store.SaveFeed(feed); err != nil {
		return nil, fmt.Errorf("saving feed: %w", err)
	}

	if err := m.store.SaveArticles(articles); err != nil {
		return nil, fmt.Errorf("saving articles: %w", err)
	}

	return feed, nil
}

func (m *Manager) RefreshFeed(feedID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	feed, err := m.store.GetFeed(feedID)
	if err != nil {
		return fmt.Errorf("getting feed: %w", err)
	}

	if time.Since(feed.LastFetched) < m.config.Feed.RefreshInterval {
		return nil
	}

	resp, updated, err := m.fetcher.Fetch(feed)
	if err != nil {
		return fmt.Errorf("fetching feed: %w", err)
	}

	if !updated || resp == nil {
		feed.LastFetched = time.Now()
		if saveErr := m.store.SaveFeed(feed); saveErr != nil {
			return fmt.Errorf("saving feed metadata: %w", saveErr)
		}
		return nil
	}

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	articles, err := m.parser.Parse(strings.NewReader(string(body)), feedID)
	if err != nil {
		return fmt.Errorf("parsing feed: %w", err)
	}

	m.fetcher.UpdateFeedMetadata(feed, resp)
	feed.UpdatedAt = time.Now()

	if err := m.store.SaveFeed(feed); err != nil {
		return fmt.Errorf("saving feed: %w", err)
	}

	if err := m.store.SaveArticles(articles); err != nil {
		return fmt.Errorf("saving articles: %w", err)
	}

	return nil
}

func (m *Manager) RefreshAllFeeds() error {
	feeds, err := m.store.GetAllFeeds()
	if err != nil {
		return fmt.Errorf("getting feeds: %w", err)
	}

	if len(feeds) == 0 {
		return nil
	}

	// Use worker pool pattern to limit concurrent operations
	const maxConcurrentRefresh = 5
	feedChan := make(chan *storage.Feed, len(feeds))
	errChan := make(chan error, len(feeds))

	// Start workers
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrentRefresh && i < len(feeds); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for feed := range feedChan {
				if refreshErr := m.RefreshFeed(feed.ID); refreshErr != nil {
					errChan <- refreshErr
				}
			}
		}()
	}

	// Send feeds to workers
	for _, feed := range feeds {
		feedChan <- feed
	}
	close(feedChan)

	// Wait for all workers to complete
	wg.Wait()
	close(errChan)

	// Collect any errors
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("refresh errors: %v", errs)
	}

	return nil
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
