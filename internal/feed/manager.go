package feed

import (
	"crypto/sha256"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

type Manager struct {
	store   *storage.Store
	fetcher *Fetcher
	parser  *Parser
	mu      sync.RWMutex
}

func NewManager(store *storage.Store) *Manager {
	return &Manager{
		store:   store,
		fetcher: NewFetcher(),
		parser:  NewParser(),
	}
}

func (m *Manager) AddFeed(url string) (*storage.Feed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	url = normalizeURL(url)
	feedID := generateFeedID(url)

	feed := &storage.Feed{
		ID:        feedID,
		URL:       url,
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

	if time.Since(feed.LastFetched) < 5*time.Minute {
		return nil
	}

	resp, updated, err := m.fetcher.Fetch(feed)
	if err != nil {
		return fmt.Errorf("fetching feed: %w", err)
	}

	if !updated || resp == nil {
		feed.LastFetched = time.Now()
		m.store.SaveFeed(feed)
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

	var wg sync.WaitGroup
	errChan := make(chan error, len(feeds))

	for _, feed := range feeds {
		wg.Add(1)
		go func(f *storage.Feed) {
			defer wg.Done()
			if err := m.RefreshFeed(f.ID); err != nil {
				errChan <- err
			}
		}(feed)
	}

	wg.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("refresh errors: %v", errs)
	}

	return nil
}

func normalizeURL(url string) string {
	url = strings.TrimSpace(url)
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		url = "https://" + url
	}
	return url
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
