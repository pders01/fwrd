package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

// mockStore implements basic store interface for testing
type mockStore struct{}

func (m *mockStore) GetArticles(feedID string, limit int) ([]*storage.Article, error) {
	return []*storage.Article{
		{ID: "article1", Title: "Test Article", Content: "Test content", FeedID: feedID},
	}, nil
}

func (m *mockStore) MarkArticleRead(articleID string, read bool) error {
	return nil
}

func createTestApp() *App {
	vp := viewport.New(40, 3) // Smaller viewport for cleaner test output
	return &App{
		store:          nil, // We'll test state transitions, not command execution
		feedList:       list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		articleList:    list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		searchList:     list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0),
		searchInput:    textinput.New(),
		viewport:       vp,
		textInput:      textinput.New(),
		view:           ViewFeeds,
		previousView:   ViewFeeds,
		cameFromSearch: false,
		searchResults:  []searchResultItem{},
		width:          80,
		height:         24,
	}
}

func TestSearchResultSelection_Article(t *testing.T) {
	app := createTestApp()
	cfg := config.TestConfig()
	keyHandler := NewKeyHandler(app, cfg)

	// Set up initial state - in search view
	app.view = ViewSearch
	app.previousView = ViewFeeds
	app.cameFromSearch = false

	// Create a test article search result
	testFeed := &storage.Feed{ID: "feed1", Title: "Test Feed"}
	testArticle := &storage.Article{ID: "article1", Title: "Test Article", Content: "Test content"}

	searchResult := searchResultItem{
		feed:      testFeed,
		article:   testArticle,
		isArticle: true,
	}

	// Select the search result
	newModel, cmd := keyHandler.selectSearchResult(searchResult)

	// Verify state changes
	if app.view != ViewReader {
		t.Errorf("Expected view to be ViewReader, got %v", app.view)
	}

	if !app.cameFromSearch {
		t.Error("Expected cameFromSearch to be true")
	}

	if app.currentArticle != testArticle {
		t.Error("Expected currentArticle to be set correctly")
	}

	if app.currentFeed != testFeed {
		t.Error("Expected currentFeed to be set correctly")
	}

	// Don't test command execution since we don't have a real store
	// Just verify that a command was returned
	if cmd == nil {
		t.Error("Expected command to be returned for rendering article")
	}

	_ = newModel // Silence unused variable warning
}

func TestSearchResultSelection_Feed(t *testing.T) {
	app := createTestApp()
	cfg := config.TestConfig()
	keyHandler := NewKeyHandler(app, cfg)

	// Set up initial state
	app.view = ViewSearch
	app.previousView = ViewFeeds

	// Create a test feed search result
	testFeed := &storage.Feed{ID: "feed1", Title: "Test Feed"}

	searchResult := searchResultItem{
		feed:      testFeed,
		article:   nil,
		isArticle: false,
	}

	// Select the search result
	_, cmd := keyHandler.selectSearchResult(searchResult)

	// Verify state changes
	if app.view != ViewArticles {
		t.Errorf("Expected view to be ViewArticles, got %v", app.view)
	}

	if app.cameFromSearch {
		t.Error("Expected cameFromSearch to be false for feed selection")
	}

	if app.currentFeed != testFeed {
		t.Error("Expected currentFeed to be set correctly")
	}

	if cmd == nil {
		t.Error("Expected command to be returned for loading articles")
	}
}

func TestNavigateBack_FromSearchResult(t *testing.T) {
	app := createTestApp()
	cfg := config.TestConfig()
	keyHandler := NewKeyHandler(app, cfg)

	// Simulate being in reader view after selecting a search result
	app.view = ViewReader
	app.cameFromSearch = true
	app.previousView = ViewFeeds

	// Navigate back
	_, _ = keyHandler.navigateBack()

	// Should go back to search view
	if app.view != ViewSearch {
		t.Errorf("Expected view to be ViewSearch, got %v", app.view)
	}

	if app.cameFromSearch {
		t.Error("Expected cameFromSearch to be reset to false")
	}

	if app.searchInput.Focused() {
		t.Error("Expected search input to be blurred when returning to search results")
	}
}

func TestNavigateBack_DoubleEscape(t *testing.T) {
	app := createTestApp()
	cfg := config.TestConfig()
	keyHandler := NewKeyHandler(app, cfg)

	// Simulate the double escape scenario
	// First: in reader from search result
	app.view = ViewReader
	app.cameFromSearch = true
	app.previousView = ViewFeeds

	// First escape - should go to search
	keyHandler.navigateBack()
	if app.view != ViewSearch {
		t.Errorf("First escape: expected ViewSearch, got %v", app.view)
	}

	// Second escape - should go to previous view
	keyHandler.navigateBack()
	if app.view != ViewFeeds {
		t.Errorf("Second escape: expected ViewFeeds, got %v", app.view)
	}
}

func TestViewStateConsistency_AfterMessages(t *testing.T) {
	app := createTestApp()

	// Test direct viewport manipulation first
	app.viewport.SetContent("direct test")
	if !strings.Contains(app.viewport.View(), "direct test") {
		t.Errorf("Direct viewport.SetContent failed. Content should contain 'direct test', got '%s'", app.viewport.View())
	}

	// Test articleRenderedMsg only processed in correct view
	app.view = ViewSearch // Wrong view
	msg := articleRenderedMsg{content: "test content"}

	// Process the message
	newModel, _ := app.Update(msg)
	app = newModel.(*App)

	// Viewport should not be updated since we're not in ViewReader
	if strings.Contains(app.viewport.View(), "test content") {
		t.Error("articleRenderedMsg was processed in wrong view state")
	}

	// Now test in correct view
	app.view = ViewReader
	t.Logf("Before update - view: %v, viewport content: '%s'", app.view, app.viewport.View())

	newModel, _ = app.Update(msg)
	app = newModel.(*App)

	t.Logf("After update - view: %v, viewport content: '%s'", app.view, app.viewport.View())

	// Now it should be processed
	actualContent := app.viewport.View()
	if !strings.Contains(actualContent, "test content") {
		t.Errorf("articleRenderedMsg was not processed in correct view state. Expected content to contain 'test content', got '%s'", actualContent)
	}
}

func TestNilValidation_SearchResults(t *testing.T) {
	app := createTestApp()
	cfg := config.TestConfig()
	keyHandler := NewKeyHandler(app, cfg)

	// Test nil article
	nilArticleResult := searchResultItem{
		feed:      nil,
		article:   nil,
		isArticle: true,
	}

	originalView := app.view
	_, cmd := keyHandler.selectSearchResult(nilArticleResult)

	// Should not change view or return command
	if app.view != originalView {
		t.Error("View should not change with nil article")
	}

	if cmd != nil {
		t.Error("Should not return command with nil article")
	}

	// Test nil feed
	nilFeedResult := searchResultItem{
		feed:      nil,
		article:   nil,
		isArticle: false,
	}

	_, cmd = keyHandler.selectSearchResult(nilFeedResult)

	if cmd != nil {
		t.Error("Should not return command with nil feed")
	}
}
