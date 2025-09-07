package tui

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/media"
	"github.com/pders01/fwrd/internal/search"
)

type KeyHandler struct {
	app         *App
	config      *config.Config
	modifierKey string
}

func NewKeyHandler(app *App, cfg *config.Config) *KeyHandler {
	modifierKey := cfg.Keys.Modifier + "+"
	return &KeyHandler{app: app, config: cfg, modifierKey: modifierKey}
}

func (kh *KeyHandler) HandleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if kh.isInTextInputMode() {
		return kh.handleTextInputMode(msg)
	}

	if model, cmd, handled := kh.handleCustomKeys(key); handled {
		return model, cmd
	}

	return kh.delegateToCharm(msg)
}

func (kh *KeyHandler) isInTextInputMode() bool {
	switch kh.app.view {
	case ViewAddFeed:
		return kh.app.textInput.Focused()
	case ViewRenameFeed:
		return kh.app.textInput.Focused()
	case ViewSearch:
		return kh.app.searchInput.Focused()
	default:
		return false
	}
}

func (kh *KeyHandler) handleTextInputMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	switch key {
	case "esc":
		return kh.navigateBack()
	case "ctrl+c":
		return kh.app, tea.Quit
	case "enter":
		return kh.handleTextInputEnter()
	case "tab", "down":

		if kh.app.view == ViewSearch {
			if len(kh.app.searchList.Items()) > 0 {
				kh.app.searchInput.Blur()

				kh.app.searchList.Select(0)
			}
			return kh.app, nil
		}

		return kh.delegateToTextInput(msg)
	case "up", "shift+tab":

		if kh.app.view == ViewSearch {

			return kh.delegateToTextInput(msg)
		}

		return kh.delegateToTextInput(msg)
	default:

		return kh.delegateToTextInput(msg)
	}
}

func (kh *KeyHandler) handleTextInputEnter() (tea.Model, tea.Cmd) {
	switch kh.app.view {
	case ViewAddFeed:
		input := strings.TrimSpace(kh.app.textInput.Value())
		if input != "" {
			if err := kh.validateFeedURL(input); err != nil {
				return kh.app, func() tea.Msg { return errorMsg{err: err} }
			}
			kh.app.setStatus("Adding feed…", 0)
			return kh.app, kh.app.addFeed(input)
		}
		return kh.app, nil

	case ViewRenameFeed:
		input := strings.TrimSpace(kh.app.textInput.Value())
		if input == "" {
			return kh.app, nil
		}
		kh.app.setStatus("Renaming…", 0)
		return kh.app, kh.app.renameFeed(input)

	case ViewSearch:
		// Select first search result if available
		if items := kh.app.searchList.Items(); len(items) > 0 {
			if i, ok := items[0].(searchResultItem); ok {
				return kh.selectSearchResult(i)
			}
		}
		return kh.app, nil

	default:
		return kh.app, nil
	}
}

// delegateToTextInput passes the key to the appropriate text input
func (kh *KeyHandler) delegateToTextInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch kh.app.view {
	case ViewAddFeed:
		newTextInput, cmd := kh.app.textInput.Update(msg)
		kh.app.textInput = newTextInput
		return kh.app, cmd

	case ViewRenameFeed:
		newTextInput, cmd := kh.app.textInput.Update(msg)
		kh.app.textInput = newTextInput
		return kh.app, cmd

	case ViewSearch:
		// Handle search input with debounce scheduling
		prev := kh.app.searchInput.Value()
		newSearchInput, cmd := kh.app.searchInput.Update(msg)
		kh.app.searchInput = newSearchInput

		newVal := kh.sanitizeSearchInput(kh.app.searchInput.Value())
		if newVal != prev {
			kh.app.pendingSearchQuery = newVal
			kh.app.searchSeq++
			seq := kh.app.searchSeq
			wait := time.Duration(kh.app.searchDebounceMillis) * time.Millisecond
			return kh.app, tea.Batch(cmd, tea.Tick(wait, func(time.Time) tea.Msg { return searchDebounceFireMsg{seq: seq} }))
		}
		return kh.app, cmd

	default:
		return kh.app, nil
	}
}

// handleCustomKeys handles only our custom action keys
func (kh *KeyHandler) handleCustomKeys(key string) (tea.Model, tea.Cmd, bool) {
	// Global custom keys
	switch key {
	case "ctrl+c", "q":
		return kh.app, tea.Quit, true
	case "esc":
		model, cmd := kh.navigateBack()
		return model, cmd, true
	case kh.modifierKey + "s":
		model, cmd := kh.enterSearchMode()
		return model, cmd, true
	}

	// View-specific custom keys
	switch kh.app.view {
	case ViewFeeds:
		return kh.handleFeedsCustomKeys(key)
	case ViewArticles:
		return kh.handleArticlesCustomKeys(key)
	case ViewReader:
		return kh.handleReaderCustomKeys(key)
	case ViewDeleteConfirm:
		return kh.handleDeleteConfirmKeys(key)
	case ViewMedia:
		return kh.handleMediaCustomKeys(key)
	default:
		return kh.app, nil, false
	}
}

// handleFeedsCustomKeys handles only custom action keys in feeds view
func (kh *KeyHandler) handleFeedsCustomKeys(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case kh.modifierKey + "n":
		kh.app.view = ViewAddFeed
		kh.app.textInput.Reset()
		kh.app.textInput.Focus()
		return kh.app, nil, true
	case kh.modifierKey + "e":
		if len(kh.app.feeds) > 0 {
			if i, ok := kh.app.feedList.SelectedItem().(feedItem); ok {
				kh.app.feedToRename = i.feed
				kh.app.view = ViewRenameFeed
				kh.app.textInput.SetValue(i.feed.Title)
				kh.app.textInput.Focus()
				return kh.app, nil, true
			}
		}
	case kh.modifierKey + "x":
		if len(kh.app.feeds) > 0 {
			if i, ok := kh.app.feedList.SelectedItem().(feedItem); ok {
				kh.app.feedToDelete = i.feed
				kh.app.view = ViewDeleteConfirm
				return kh.app, nil, true
			}
		}
	case kh.modifierKey + "r":
		kh.app.setStatus("Refreshing…", 0)
		return kh.app, tea.Batch(kh.app.startSpinner("Refreshing…"), kh.app.refreshFeeds()), true
	}
	return kh.app, nil, false
}

// handleArticlesCustomKeys handles only custom action keys in articles view
func (kh *KeyHandler) handleArticlesCustomKeys(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case kh.modifierKey + "o":
		if i, ok := kh.app.articleList.SelectedItem().(articleItem); ok {
			if i.article.URL != "" {
				return kh.app, kh.openURL(i.article.URL), true
			}
		}
		return kh.app, nil, true
	case kh.modifierKey + "m":
		if i, ok := kh.app.articleList.SelectedItem().(articleItem); ok {
			return kh.app, kh.app.toggleRead(i.article), true
		}
	}
	return kh.app, nil, false
}

// handleReaderCustomKeys handles only custom action keys in reader view
func (kh *KeyHandler) handleReaderCustomKeys(key string) (tea.Model, tea.Cmd, bool) {
	if key == kh.modifierKey+"o" {
		if kh.app.currentArticle != nil {
			// If there are multiple media URLs, show media list
			if len(kh.app.currentArticle.MediaURLs) > 1 {
				model, cmd := kh.openMediaList()
				return model, cmd, true
			}

			// If there's only one media URL or just the article URL, open it directly
			var url string
			if len(kh.app.currentArticle.MediaURLs) == 1 {
				url = kh.app.currentArticle.MediaURLs[0]
			} else if kh.app.currentArticle.URL != "" {
				url = kh.app.currentArticle.URL
			}

			if url != "" {
				return kh.app, kh.openURL(url), true
			}
		}
		return kh.app, nil, true
	}
	return kh.app, nil, false
}

// delegateToCharm lets Charm handle all keys we don't intercept
func (kh *KeyHandler) delegateToCharm(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd

	switch kh.app.view {
	case ViewFeeds:
		// Let the feed list handle enter, navigation, filtering, help, etc.
		kh.app.feedList, cmd = kh.app.feedList.Update(msg)
		// Handle enter key for feed selection
		if msg.String() == "enter" {
			if i, ok := kh.app.feedList.SelectedItem().(feedItem); ok {
				kh.app.currentFeed = i.feed
				kh.app.view = ViewArticles
				return kh.app, kh.app.loadArticles(i.feed.ID)
			}
		}
		return kh.app, cmd

	case ViewArticles:
		kh.app.articleList, cmd = kh.app.articleList.Update(msg)
		// Handle enter key for article selection
		if msg.String() == "enter" {
			if i, ok := kh.app.articleList.SelectedItem().(articleItem); ok {
				kh.app.currentArticle = i.article
				kh.app.cameFromSearch = false
				kh.app.loadingArticle = true // Set loading flag
				kh.app.setStatus("Loading article…", 0)
				kh.app.view = ViewReader
				// Mark article as read when opened
				markReadCmd := kh.app.markArticleRead(i.article)
				renderCmd := kh.app.renderArticle(i.article)
				return kh.app, tea.Batch(kh.app.startSpinner("Loading article…"), markReadCmd, renderCmd)
			}
		}
		return kh.app, cmd

	case ViewSearch:
		// Handle focus switching when not in text input mode
		if !kh.app.searchInput.Focused() {
			switch msg.String() {
			case "tab", "shift+tab":
				// Tab always returns to search input
				kh.app.searchInput.Focus()
				return kh.app, nil
			case "up":
				// Navigate up in results, or refocus input if at top
				if len(kh.app.searchList.Items()) > 0 && kh.app.searchList.Index() == 0 {
					kh.app.searchInput.Focus()
					return kh.app, nil
				}
			case "/", "i":
				// Quick shortcuts to refocus search input
				kh.app.searchInput.Focus()
				return kh.app, nil
			}
		}

		kh.app.searchList, cmd = kh.app.searchList.Update(msg)
		// Handle enter key for search result selection
		if msg.String() == "enter" && !kh.app.searchInput.Focused() {
			if i, ok := kh.app.searchList.SelectedItem().(searchResultItem); ok {
				return kh.selectSearchResult(i)
			}
		}
		return kh.app, cmd

	case ViewReader:
		// Let viewport handle scrolling
		kh.app.viewport, cmd = kh.app.viewport.Update(msg)
		return kh.app, cmd

	case ViewMedia:
		// Let the media list handle navigation
		kh.app.mediaList, cmd = kh.app.mediaList.Update(msg)
		// Handle enter key for media selection
		if msg.String() == "enter" {
			if i, ok := kh.app.mediaList.SelectedItem().(mediaItem); ok {
				return kh.app, kh.openURL(i.url)
			}
		}
		return kh.app, cmd

	default:
		return kh.app, nil
	}
}

// handleDeleteConfirmKeys handles keys in delete confirmation view
func (kh *KeyHandler) handleMediaCustomKeys(key string) (tea.Model, tea.Cmd, bool) {
	switch key {
	case "enter":
		// Open the selected media item
		if item, ok := kh.app.mediaList.SelectedItem().(mediaItem); ok {
			return kh.app, kh.openURL(item.url), true
		}
		return kh.app, nil, true
	case kh.modifierKey + "o":
		// Also handle ctrl+o to open
		if item, ok := kh.app.mediaList.SelectedItem().(mediaItem); ok {
			return kh.app, kh.openURL(item.url), true
		}
		return kh.app, nil, true
	}
	return kh.app, nil, false
}

func (kh *KeyHandler) handleDeleteConfirmKeys(key string) (tea.Model, tea.Cmd, bool) {
	if key == "enter" {
		if kh.app.feedToDelete != nil {
			kh.app.setStatus("Deleting…", 0)
			return kh.app, kh.app.deleteFeed(kh.app.feedToDelete.ID), true
		}
	}
	return kh.app, nil, false
}

// selectSearchResult handles selection of search results
func (kh *KeyHandler) selectSearchResult(result searchResultItem) (tea.Model, tea.Cmd) {
	if result.isArticle {
		// Validate article data
		if result.article == nil {
			return kh.app, nil
		}
		kh.app.currentArticle = result.article
		kh.app.currentFeed = result.feed
		kh.app.cameFromSearch = true
		kh.app.loadingArticle = true // Set loading flag
		kh.app.setStatus("Loading article…", 0)
		kh.app.view = ViewReader
		// Mark article as read when opened
		markReadCmd := kh.app.markArticleRead(result.article)
		renderCmd := kh.app.renderArticle(result.article)
		return kh.app, tea.Batch(kh.app.startSpinner("Loading article…"), markReadCmd, renderCmd)
	}

	// Validate feed data
	if result.feed == nil {
		return kh.app, nil
	}
	kh.app.currentFeed = result.feed
	kh.app.cameFromSearch = false
	kh.app.view = ViewArticles
	return kh.app, kh.app.loadArticles(result.feed.ID)
}

// navigateBack implements smart back navigation
func (kh *KeyHandler) navigateBack() (tea.Model, tea.Cmd) {
	switch kh.app.view {
	case ViewAddFeed, ViewDeleteConfirm, ViewRenameFeed:
		kh.app.view = ViewFeeds
		kh.app.feedToDelete = nil
		kh.app.feedToRename = nil
		return kh.app, nil

	case ViewSearch:
		kh.app.view = kh.app.previousView
		kh.app.searchInput.Reset()
		kh.app.searchResults = []searchResultItem{}
		kh.app.searchList.SetItems([]list.Item{})
		return kh.app, nil

	case ViewMedia:
		kh.app.view = kh.app.previousView
		kh.app.mediaURLs = []string{}
		kh.app.mediaList.SetItems([]list.Item{})
		return kh.app, nil

	case ViewArticles:
		kh.app.view = ViewFeeds
		return kh.app, nil

	case ViewReader:
		if kh.app.cameFromSearch {
			kh.app.view = ViewSearch
			kh.app.cameFromSearch = false
			// Focus search results list, not input, for quick navigation
			kh.app.searchInput.Blur()
			return kh.app, nil
		}
		kh.app.view = ViewArticles
		return kh.app, nil

	default:
		return kh.app, tea.Quit
	}
}

// enterSearchMode transitions to search view
func (kh *KeyHandler) enterSearchMode() (tea.Model, tea.Cmd) {
	kh.app.previousView = kh.app.view
	kh.app.view = ViewSearch
	kh.app.searchInput.Reset()
	kh.app.searchInput.Focus()
	kh.app.searchResults = []searchResultItem{}
	kh.app.searchList.SetItems([]list.Item{})
	// Debug: show which search engine is active and doc count if available
	engineName := fmt.Sprintf("%T", kh.app.searchEngine)
	if ds, ok := kh.app.searchEngine.(search.DebugStatser); ok {
		if n, err := ds.DocCount(); err == nil {
			kh.app.setStatus(fmt.Sprintf("Search: %s • idx: %d", engineName, n), 0)
			return kh.app, nil
		}
	}
	kh.app.setStatus(fmt.Sprintf("Search: %s", engineName), 0)
	return kh.app, nil
}

// validateFeedURL validates that a URL is suitable for RSS feeds
func (kh *KeyHandler) validateFeedURL(input string) error {
	input = strings.TrimSpace(input)

	// Length validation
	if input == "" {
		return fmt.Errorf("URL cannot be empty")
	}
	if len(input) > 2048 {
		return fmt.Errorf("URL too long (max 2048 characters)")
	}

	// Add protocol if missing
	if !strings.HasPrefix(input, "http://") && !strings.HasPrefix(input, "https://") {
		input = "https://" + input
	}

	// Parse URL
	parsedURL, err := url.Parse(input)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check scheme
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("URL must use http or https protocol")
	}

	// Check host
	if parsedURL.Host == "" {
		return fmt.Errorf("URL must have a valid hostname")
	}

	// Basic sanitization check
	if strings.Contains(input, "<") || strings.Contains(input, ">") {
		return fmt.Errorf("URL contains invalid characters")
	}

	return nil
}

// sanitizeSearchInput sanitizes and limits search input length
func (kh *KeyHandler) sanitizeSearchInput(input string) string {
	input = strings.TrimSpace(input)

	// Length limit for search queries
	if len(input) > 256 {
		input = input[:256]
	}

	// Remove newlines/tabs
	input = strings.ReplaceAll(input, "\n", " ")
	input = strings.ReplaceAll(input, "\r", " ")
	input = strings.ReplaceAll(input, "\t", " ")

	// Collapse multiple spaces
	for strings.Contains(input, "  ") {
		input = strings.ReplaceAll(input, "  ", " ")
	}

	return strings.TrimSpace(input)
}

// openURL opens a URL using the launcher and handles errors appropriately
func (kh *KeyHandler) openMediaList() (tea.Model, tea.Cmd) {
	if kh.app.currentArticle == nil || len(kh.app.currentArticle.MediaURLs) == 0 {
		return kh.app, nil
	}

	// Prepare media items for the list
	items := make([]list.Item, len(kh.app.currentArticle.MediaURLs))
	detector, _ := media.NewTypeDetector()

	for i, url := range kh.app.currentArticle.MediaURLs {
		mediaType := media.TypeUnknown
		if detector != nil {
			mediaType = detector.DetectType(url)
		}
		items[i] = mediaItem{
			url:       url,
			mediaType: mediaType,
			index:     i,
			total:     len(kh.app.currentArticle.MediaURLs),
		}
	}

	kh.app.mediaList.SetItems(items)
	kh.app.mediaURLs = kh.app.currentArticle.MediaURLs
	kh.app.previousView = kh.app.view
	kh.app.view = ViewMedia

	// Set title with article name
	title := "› media"
	if kh.app.currentArticle != nil && kh.app.currentArticle.Title != "" {
		// Truncate title if too long
		articleTitle := kh.app.currentArticle.Title
		if len(articleTitle) > 50 {
			articleTitle = articleTitle[:47] + "..."
		}
		title = fmt.Sprintf("› media from: %s", articleTitle)
	}
	kh.app.mediaList.Title = title

	return kh.app, nil
}

func (kh *KeyHandler) openURL(url string) tea.Cmd {
	return func() tea.Msg {
		if err := kh.app.launcher.Open(url); err != nil {
			return errorMsg{err: fmt.Errorf("failed to open %s: %w", url, err)}
		}
		return nil
	}
}

// GetHelpForCurrentView returns only our custom help text (Charm handles the rest)
func (kh *KeyHandler) GetHelpForCurrentView() []string {
	switch kh.app.view {
	case ViewFeeds:
		help := []string{kh.modifierKey + "n: new", kh.modifierKey + "r: refresh", kh.modifierKey + "s: search"}
		if len(kh.app.feeds) > 0 {
			help = append(help, kh.modifierKey+"e: rename", kh.modifierKey+"x: delete")
		}
		return help

	case ViewArticles:
		return []string{kh.modifierKey + "o: open", kh.modifierKey + "m: toggle read", kh.modifierKey + "s: search"}

	case ViewReader:
		return []string{kh.modifierKey + "o: open media", kh.modifierKey + "s: search"}

	case ViewSearch:
		return []string{kh.modifierKey + "s: search"}

	case ViewMedia:
		return []string{"enter: open", kh.modifierKey + "o: open", "esc: back"}

	case ViewAddFeed:
		return []string{"enter: add", "esc: cancel"}

	case ViewRenameFeed:
		return []string{"enter: rename", "esc: cancel"}

	case ViewDeleteConfirm:
		return []string{"enter: confirm", "esc: cancel"}

	default:
		return []string{}
	}
}
