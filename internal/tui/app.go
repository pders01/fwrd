package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/media"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

// Use branded styles from branding.go

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type App struct {
	store           *storage.Store
	fetcher         *feed.Fetcher
	parser          *feed.Parser
	launcher        *media.Launcher
	searchEngine    *search.Engine
	keyHandler      *KeyHandler
	feedList        list.Model
	articleList     list.Model
	searchList      list.Model
	searchInput     textinput.Model
	viewport        viewport.Model
	textInput       textinput.Model
	help            help.Model
	view            View
	previousView    View
	cameFromSearch  bool // Track if current article was selected from search
	feeds           []*storage.Feed
	articles        []*storage.Article
	currentFeed     *storage.Feed
	currentArticle  *storage.Article
	feedToDelete    *storage.Feed
	searchResults   []searchResultItem
	width           int
	height          int
	err             error
	glamourRenderer *glamour.TermRenderer
	rendererWidth   int  // Track the width used for the renderer
	loadingArticle  bool // Track if we're loading an article
}

func NewApp(store *storage.Store) *App {
	feedList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	feedList.Title = "› feeds"
	feedList.SetShowStatusBar(false)
	feedList.SetFilteringEnabled(true)
	feedList.SetShowHelp(true) // Let Charm show native help

	articleList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	articleList.Title = "› articles"
	articleList.SetShowStatusBar(false)
	articleList.SetFilteringEnabled(true)
	articleList.SetShowHelp(true) // Let Charm show native help

	searchList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	searchList.Title = "› search results"
	searchList.SetShowStatusBar(false)
	searchList.SetShowHelp(false) // No native filtering for search results
	searchList.SetFilteringEnabled(false)

	vp := viewport.New(0, 0)

	ti := textinput.New()
	ti.Placeholder = "Enter feed URL..."
	ti.Focus()

	si := textinput.New()
	si.Placeholder = "Search feeds and articles..."

	app := &App{
		store:          store,
		fetcher:        feed.NewFetcher(),
		parser:         feed.NewParser(),
		launcher:       media.NewLauncher(),
		searchEngine:   search.NewEngine(store),
		feedList:       feedList,
		articleList:    articleList,
		searchList:     searchList,
		searchInput:    si,
		viewport:       vp,
		textInput:      ti,
		help:           help.New(),
		view:           ViewFeeds,
		previousView:   ViewFeeds,            // Initialize previous view
		cameFromSearch: false,                // Initialize navigation flag
		searchResults:  []searchResultItem{}, // Initialize empty search results
	}

	// Initialize key handler after app is created
	app.keyHandler = NewKeyHandler(app)

	return app
}

// getRenderer returns the glamour renderer, creating or updating it if needed
func (a *App) getRenderer() (*glamour.TermRenderer, error) {
	// Calculate desired width
	wordWrapWidth := (a.width * 9) / 10
	if wordWrapWidth > 120 {
		wordWrapWidth = 120 // maximum for readability
	}
	if wordWrapWidth < 40 {
		wordWrapWidth = 40 // minimum for readability
	}
	// For very narrow screens, use almost full width
	if a.width < 50 {
		wordWrapWidth = a.width - 4
		if wordWrapWidth < 20 {
			wordWrapWidth = 20
		}
	}

	// Create new renderer if we don't have one or if width changed significantly
	if a.glamourRenderer == nil || abs(a.rendererWidth-wordWrapWidth) > 10 {
		r, err := glamour.NewTermRenderer(
			glamour.WithAutoStyle(),
			glamour.WithWordWrap(wordWrapWidth),
		)
		if err != nil {
			return nil, err
		}
		a.glamourRenderer = r
		a.rendererWidth = wordWrapWidth
	}

	return a.glamourRenderer, nil
}

// Helper function for absolute value
func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(
		a.loadFeeds(),
		tea.EnterAltScreen,
	)
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.feedList.SetSize(msg.Width, msg.Height-3)
		a.articleList.SetSize(msg.Width, msg.Height-3)
		// Search view layout:
		// - Header: 1 line
		// - Blank: 1 line
		// - Search input: 3 lines (with border)
		// - Help text: 1 line
		// - Blank: 1 line
		// - Status bar: 3 lines
		// Total overhead: 10 lines
		searchListHeight := msg.Height - 10
		if searchListHeight < 5 {
			searchListHeight = 5 // Minimum height
		}
		a.searchList.SetSize(msg.Width, searchListHeight)
		a.viewport.Width = msg.Width
		a.viewport.Height = msg.Height - 3

		// Make text inputs responsive
		inputWidth := msg.Width - 4 // Leave some padding
		if inputWidth < 20 {
			inputWidth = msg.Width
		}
		a.textInput.Width = inputWidth
		// Don't set searchInput width here - it's set dynamically in View()

	case tea.KeyMsg:
		// Delegate all key handling to the key handler
		return a.keyHandler.HandleKey(msg)

	case feedsLoadedMsg:
		a.feeds = msg.feeds
		items := make([]list.Item, len(msg.feeds))
		for i, f := range msg.feeds {
			items[i] = feedItem{feed: f}
		}
		a.feedList.SetItems(items)

	case articlesLoadedMsg:
		// Only process if we're in articles view to prevent stale state
		if a.view == ViewArticles {
			a.articles = msg.articles
			items := make([]list.Item, len(msg.articles))
			for i, art := range msg.articles {
				items[i] = articleItem{article: art}
			}
			a.articleList.SetItems(items)
		}

	case articleRenderedMsg:
		// Only process if we're in reader view to prevent stale content
		if a.view == ViewReader {
			a.viewport.SetContent(msg.content)
			a.viewport.GotoTop()
			a.loadingArticle = false // Article has finished loading
		}

	case feedAddedMsg:
		if msg.err != nil {
			a.err = msg.err
		} else {
			a.view = ViewFeeds
			return a, a.loadFeeds()
		}

	case feedDeletedMsg:
		if msg.err != nil {
			a.err = msg.err
		} else {
			a.view = ViewFeeds
			a.feedToDelete = nil
			return a, a.loadFeeds()
		}

	case searchResultsMsg:
		// Only process search results if we're still in search view
		if a.view == ViewSearch {
			a.searchResults = msg.results
			items := make([]list.Item, len(msg.results))
			for i, result := range msg.results {
				items[i] = result
			}
			a.searchList.SetItems(items)
		}
		// Ignore search results if we've left search view to prevent state corruption

	case errorMsg:
		a.err = msg.err
	}

	switch a.view {
	case ViewFeeds:
		newListModel, cmd := a.feedList.Update(msg)
		a.feedList = newListModel
		cmds = append(cmds, cmd)
	case ViewArticles:
		newListModel, cmd := a.articleList.Update(msg)
		a.articleList = newListModel
		cmds = append(cmds, cmd)
	case ViewReader:
		// Only pass specific message types to viewport, not our custom messages
		switch msg.(type) {
		case tea.KeyMsg, tea.WindowSizeMsg, tea.MouseMsg:
			newViewport, cmd := a.viewport.Update(msg)
			a.viewport = newViewport
			cmds = append(cmds, cmd)
		}
	case ViewAddFeed:
		newTextInput, cmd := a.textInput.Update(msg)
		a.textInput = newTextInput
		cmds = append(cmds, cmd)
	case ViewDeleteConfirm:
		// No child components to update in delete confirmation view
	case ViewSearch:
		newSearchInput, cmd := a.searchInput.Update(msg)
		a.searchInput = newSearchInput
		cmds = append(cmds, cmd)

		// Update search list
		newSearchList, listCmd := a.searchList.Update(msg)
		a.searchList = newSearchList
		cmds = append(cmds, listCmd)

		// Perform search when input changes
		searchQuery := a.searchInput.Value()
		if searchQuery != "" && len(searchQuery) > 1 {
			searchCmd := a.performSearch(searchQuery)
			cmds = append(cmds, searchCmd)
		}
	}

	return a, tea.Batch(cmds...)
}

func (a *App) updateSearchInput(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd

	// Update search input
	newSearchInput, cmd := a.searchInput.Update(msg)
	a.searchInput = newSearchInput
	cmds = append(cmds, cmd)

	// Update search list
	newSearchList, listCmd := a.searchList.Update(msg)
	a.searchList = newSearchList
	cmds = append(cmds, listCmd)

	// Perform search when input changes (only if still in search view)
	searchQuery := a.searchInput.Value()
	if searchQuery != "" && len(searchQuery) > 1 && a.view == ViewSearch {
		// Use context-aware search based on where we came from
		var searchCmd tea.Cmd
		if a.previousView == ViewReader && a.currentArticle != nil {
			searchCmd = a.performSearchWithContext(searchQuery, "article")
		} else {
			searchCmd = a.performSearch(searchQuery)
		}
		cmds = append(cmds, searchCmd)
	}

	return tea.Batch(cmds...)
}

func (a *App) View() string {
	var content string

	switch a.view {
	case ViewFeeds:
		if len(a.feeds) == 0 {
			content = lipgloss.NewStyle().
				Width(a.width).
				Height(a.height).
				Align(lipgloss.Center, lipgloss.Center).
				Render(GetWelcomeMessage())
		} else {
			content = a.feedList.View()
		}
	case ViewArticles:
		content = a.articleList.View()
	case ViewReader:
		// Check if we're still waiting for content to be rendered
		if a.loadingArticle {
			// Show loading state while article is rendering
			content = lipgloss.NewStyle().
				Padding(2, 4).
				Foreground(MutedColor).
				Render("Loading article...")
		} else {
			content = a.viewport.View()
		}
	case ViewAddFeed:
		content = lipgloss.NewStyle().
			Width(a.width).
			Height(a.height).
			Align(lipgloss.Center, lipgloss.Center).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Center,
					TitleStyle.Render("› add feed"),
					"",
					a.textInput.View(),
					"",
					HelpStyle.Render("Press Enter to add, Esc to cancel"),
				),
			)
	case ViewDeleteConfirm:
		feedName := "Unknown Feed"
		if a.feedToDelete != nil {
			feedName = a.feedToDelete.Title
			if feedName == "" {
				feedName = a.feedToDelete.URL
			}
		}

		// Make modal responsive to viewport size
		// Responsive modal width - use 80% of screen width, with min 20 and max based on content
		modalWidth := (a.width * 4) / 5 // 80% of width
		if modalWidth < 20 {
			modalWidth = a.width - 4 // Leave minimal margins if very narrow
			if modalWidth < 15 {
				modalWidth = a.width // Use full width on tiny screens
			}
		}

		// Truncate feed name if too long for small screens
		if len(feedName) > modalWidth-4 {
			feedName = feedName[:modalWidth-7] + "..."
		}

		content = lipgloss.NewStyle().
			Width(a.width).
			Height(a.height).
			Align(lipgloss.Center, lipgloss.Center).
			Render(
				lipgloss.JoinVertical(
					lipgloss.Center,
					lipgloss.NewStyle().
						Foreground(ErrorColor).
						Bold(true).
						Render("⚠ Delete Feed"),
					"",
					lipgloss.NewStyle().
						Foreground(TextColor).
						Width(modalWidth).
						Align(lipgloss.Center).
						Render("Delete this feed?"),
					"",
					lipgloss.NewStyle().
						Foreground(UnreadColor).
						Bold(true).
						Width(modalWidth).
						Align(lipgloss.Center).
						Render(feedName),
					"",
					lipgloss.NewStyle().
						Foreground(MutedColor).
						Width(modalWidth).
						Align(lipgloss.Center).
						Render("This removes all articles."),
					"",
					"",
					HelpStyle.Render("Enter: confirm • Esc: cancel"),
				),
			)
	case ViewSearch:
		// Constrain search input to fit viewport
		searchInputWidth := a.width - 8 // Account for border, padding, and margins
		if searchInputWidth < 10 {
			searchInputWidth = a.width - 4
		}
		a.searchInput.Width = searchInputWidth

		// Visual indicator for which element has focus
		inputBorderColor := MutedColor
		if a.searchInput.Focused() {
			inputBorderColor = AccentColor
		}

		searchInput := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(inputBorderColor).
			Padding(0, 1).
			Width(searchInputWidth + 4). // Add border and padding width
			Render(a.searchInput.View())

		// Show context-aware search header
		searchHeader := "› search"
		if a.previousView == ViewReader && a.currentArticle != nil {
			searchHeader = "› search in article: " + a.currentArticle.Title
			if len(searchHeader) > a.width-10 {
				searchHeader = "› search in article: " + a.currentArticle.Title[:a.width-25] + "…"
			}
		}

		// Show help text based on focus state
		helpText := ""
		if a.searchInput.Focused() {
			helpText = "Type to search • Tab/↓: results • Esc: back"
		} else if len(a.searchList.Items()) > 0 {
			helpText = "↑↓: navigate • Enter: select • Tab/↑: search box • Esc: back"
		} else {
			helpText = "No results found • Tab/↑: search box • Esc: back"
		}

		searchContent := lipgloss.JoinVertical(
			lipgloss.Top,
			lipgloss.NewStyle().
				Foreground(SecondaryColor).
				Bold(true).
				Render(searchHeader),
			"",
			searchInput,
			lipgloss.NewStyle().
				Foreground(MutedColor).
				Render(helpText),
			"",
			a.searchList.View(),
		)

		// Ensure content fits within viewport
		content = lipgloss.NewStyle().
			Width(a.width).
			Height(a.height - 3). // Account for status bar
			MaxHeight(a.height - 3).
			Render(searchContent)
	}

	// Add a minimal custom status bar for visual separation
	customStatus := a.getCustomStatusBar()
	if customStatus != "" {
		// Add visual separator between native help and custom status
		separatorWidth := a.width - 2
		if separatorWidth < 0 {
			separatorWidth = 0
		}
		separator := lipgloss.NewStyle().
			Foreground(MutedColor).
			Render("─" + strings.Repeat("─", separatorWidth))

		return lipgloss.JoinVertical(lipgloss.Top, content, separator, customStatus)
	}

	return content
}

func (a *App) getCustomStatusBar() string {
	// Use key handler for help text - only show our custom commands
	commands := a.keyHandler.GetHelpForCurrentView()

	// Don't show status bar if no custom commands (let Charm handle it)
	if len(commands) == 0 {
		return ""
	}

	// Handle errors
	if a.err != nil {
		errorMsg := lipgloss.NewStyle().
			Foreground(ErrorColor).
			Bold(true).
			Render(fmt.Sprintf("✗ %v", a.err))

		return lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 1).
			Foreground(MutedColor).
			Render(errorMsg)
	}

	// Create a minimal status bar with just custom commands
	if len(commands) > 0 {
		commandText := strings.Join(commands, " • ")
		return lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 1).
			Foreground(MutedColor).
			Render(commandText)
	}

	return ""
}

type feedItem struct {
	feed *storage.Feed
}

func (i feedItem) Title() string       { return i.feed.Title }
func (i feedItem) Description() string { return i.feed.Description }
func (i feedItem) FilterValue() string { return i.feed.Title }

type articleItem struct {
	article *storage.Article
}

func (i articleItem) Title() string {
	if i.article.Read {
		return ReadItemStyle.Render(i.article.Title)
	}
	return UnreadItemStyle.Render("● " + i.article.Title)
}

func (i articleItem) Description() string {
	desc := i.article.Description
	// Make description length responsive to viewport width
	// Use a reasonable default description length for list items
	maxDescLength := 80
	if maxDescLength > 40 { // minimum readable length
		if len(desc) > maxDescLength {
			desc = desc[:maxDescLength] + "…"
		}
	}

	timeStr := ""
	if !i.article.Published.IsZero() {
		timeStr = TimeStyle.Render(" • " + i.article.Published.Format("Jan 2, 15:04"))
	}

	return lipgloss.NewStyle().
		Foreground(MutedColor).
		Render(desc) + timeStr
}

func (i articleItem) FilterValue() string { return i.article.Title }

type searchResultItem struct {
	feed      *storage.Feed
	article   *storage.Article
	isArticle bool
}

func (i searchResultItem) Title() string {
	if i.isArticle {
		prefix := "📄 "
		if i.article.Read {
			return ReadItemStyle.Render(prefix + i.article.Title)
		}
		return UnreadItemStyle.Render(prefix + i.article.Title)
	} else {
		return lipgloss.NewStyle().
			Foreground(SecondaryColor).
			Bold(true).
			Render("📁 " + i.feed.Title)
	}
}

func (i searchResultItem) Description() string {
	if i.isArticle {
		desc := i.article.Description
		// Make search result description responsive
		// Use a reasonable description length for search results
		maxDescLength := 50
		if len(desc) > maxDescLength {
			desc = desc[:maxDescLength] + "…"
		}

		// Show which feed this article belongs to
		feedName := "Unknown Feed"
		if i.feed != nil {
			feedName = i.feed.Title
			if feedName == "" {
				feedName = i.feed.URL
			}
		}

		timeStr := ""
		if !i.article.Published.IsZero() {
			timeStr = i.article.Published.Format("Jan 2")
		}

		return lipgloss.NewStyle().
			Foreground(MutedColor).
			Render(desc + " • from " + feedName + " • " + timeStr)
	} else {
		return lipgloss.NewStyle().
			Foreground(MutedColor).
			Render(i.feed.URL)
	}
}

func (i searchResultItem) FilterValue() string {
	if i.isArticle {
		return i.article.Title + " " + i.article.Description
	}
	return i.feed.Title + " " + i.feed.Description
}

type feedsLoadedMsg struct {
	feeds []*storage.Feed
}

type articlesLoadedMsg struct {
	articles []*storage.Article
}

type articleRenderedMsg struct {
	content string
}

type feedAddedMsg struct {
	err error
}

type errorMsg struct {
	err error
}

type feedDeletedMsg struct {
	err error
}

type searchResultsMsg struct {
	results []searchResultItem
}
