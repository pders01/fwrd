package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/media"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

type App struct {
	config          *config.Config
	store           *storage.Store
	fetcher         *feed.Fetcher
	parser          *feed.Parser
	launcher        *media.Launcher
	searchEngine    search.Searcher
	keyHandler      *KeyHandler
	feedList        list.Model
	articleList     list.Model
	searchList      list.Model
	mediaList       list.Model
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
	feedToRename    *storage.Feed
	searchResults   []searchResultItem
	mediaURLs       []string // Current media URLs being displayed
	width           int
	height          int
	err             error
	glamourRenderer *glamour.TermRenderer
	rendererWidth   int  // Track the width used for the renderer
	loadingArticle  bool // Track if we're loading an article

	// Debounced search state
	searchSeq            int
	pendingSearchQuery   string
	searchDebounceMillis int

	// Transient status bar message
	statusText  string
	statusKind  StatusKind
	statusUntil time.Time

	// Subtle spinner in status bar for long ops
	statusSpinner spinner.Model
	spinnerActive bool
	spinnerLabel  string
	spinnerKind   StatusKind
}

func NewApp(store *storage.Store, cfg *config.Config) *App {
	feedList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	feedList.Title = ""
	feedList.SetShowStatusBar(false)
	feedList.SetFilteringEnabled(true)
	feedList.SetShowHelp(true) // Let Charm show native help

	articleList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	articleList.Title = ""
	articleList.SetShowStatusBar(false)
	articleList.SetFilteringEnabled(true)
	articleList.SetShowHelp(true) // Let Charm show native help

	searchList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	searchList.Title = "› search results"
	searchList.SetShowStatusBar(false)
	searchList.SetShowHelp(false) // No native filtering for search results
	searchList.SetFilteringEnabled(false)

	mediaList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	mediaList.Title = "› media"
	mediaList.SetShowStatusBar(false)
	mediaList.SetFilteringEnabled(false)
	mediaList.SetShowHelp(true)

	vp := viewport.New(0, 0)

	ti := textinput.New()
	ti.Placeholder = "Enter feed URL..."
	ti.Focus()

	si := textinput.New()
	si.Placeholder = "Search feeds and articles..."

	app := &App{
		config:   cfg,
		store:    store,
		fetcher:  feed.NewFetcher(cfg),
		parser:   feed.NewParser(),
		launcher: media.NewLauncher(cfg),
		// searchEngine set below (Bleve if available, otherwise fallback)
		feedList:             feedList,
		articleList:          articleList,
		searchList:           searchList,
		mediaList:            mediaList,
		searchInput:          si,
		viewport:             vp,
		textInput:            ti,
		help:                 help.New(),
		view:                 ViewFeeds,
		previousView:         ViewFeeds,            // Initialize previous view
		cameFromSearch:       false,                // Initialize navigation flag
		searchResults:        []searchResultItem{}, // Initialize empty search results
		searchDebounceMillis: 200,
	}

	// Prefer Bleve-backed engine if available (build with -tags=bleve)
	// Index path is based on DB path with .bleve extension
	idxPath := func() string {
		dbPath := cfg.Database.Path
		if dbPath == "" {
			return "fwrd.bleve"
		}
		// If using default ~/.fwrd.db, place index at ~/.fwrd/index.bleve
		home, _ := os.UserHomeDir()
		if filepath.Base(dbPath) == ".fwrd.db" && filepath.Dir(dbPath) == home {
			return filepath.Join(home, ".fwrd", "index.bleve")
		}
		// Special case for tests: in-memory DB path
		if dbPath == ":memory:" {
			return filepath.Join(os.TempDir(), fmt.Sprintf("fwrd-index-%d.bleve", time.Now().UnixNano()))
		}
		base := strings.TrimSuffix(dbPath, filepath.Ext(dbPath))
		return base + ".bleve"
	}()
	if be, err := search.NewBleveEngine(store, idxPath); err == nil && be != nil {
		app.searchEngine = be
	} else {
		app.searchEngine = search.NewEngine(store)
	}

	app.keyHandler = NewKeyHandler(app, cfg)

	// Initialize status spinner (subtle)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(MutedColor)
	app.statusSpinner = sp

	return app
}

func (a *App) getRenderer() (*glamour.TermRenderer, error) {
	wordWrapWidth := (a.width * 9) / 10
	if wordWrapWidth > 120 {
		wordWrapWidth = 120 // maximum for readability
	}
	if wordWrapWidth < 40 {
		wordWrapWidth = 40 // minimum for readability
	}
	if a.width < 50 {
		wordWrapWidth = a.width - 4
		if wordWrapWidth < 20 {
			wordWrapWidth = 20
		}
	}

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

	// Keep spinner animated
	var spCmd tea.Cmd
	a.statusSpinner, spCmd = a.statusSpinner.Update(msg)
	if a.spinnerActive && spCmd != nil {
		cmds = append(cmds, spCmd)
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width = msg.Width
		a.height = msg.Height
		a.feedList.SetSize(msg.Width, msg.Height-3)
		a.articleList.SetSize(msg.Width, msg.Height-3)
		// Search view layout requires 10 lines for UI chrome
		searchListHeight := msg.Height - 10
		if searchListHeight < 5 {
			searchListHeight = 5 // Minimum height
		}
		a.searchList.SetSize(msg.Width, searchListHeight)
		a.mediaList.SetSize(msg.Width, msg.Height-3)
		a.viewport.Width = msg.Width
		a.viewport.Height = msg.Height - 3

		inputWidth := msg.Width - 4
		if inputWidth < 20 {
			inputWidth = msg.Width
		}
		a.textInput.Width = inputWidth

	case tea.KeyMsg:
		return a.keyHandler.HandleKey(msg)

	case feedsLoadedMsg:
		a.feeds = msg.feeds
		items := make([]list.Item, len(msg.feeds))
		for i, f := range msg.feeds {
			items[i] = feedItem{feed: f}
		}
		a.feedList.SetItems(items)

	case articlesLoadedMsg:
		if a.view == ViewArticles {
			a.articles = msg.articles
			items := make([]list.Item, len(msg.articles))
			for i, art := range msg.articles {
				items[i] = articleItem{article: art}
			}
			a.articleList.SetItems(items)
		}

	case articleRenderedMsg:
		if a.view == ViewReader {
			a.viewport.SetContent(msg.content)
			a.viewport.GotoTop()
			a.loadingArticle = false // Article has finished loading
			a.stopSpinner()
		}

	case feedAddedMsg:
		if msg.err != nil {
			a.err = msg.err
		} else {
			a.view = ViewFeeds
			a.setStatusWithKind(MsgAddedFeed(msg.title, msg.added), StatusSuccess, 0)
			cmd := a.loadFeeds()
			return a, cmd
		}
	case feedRenamedMsg:
		if msg.err != nil {
			a.err = msg.err
		} else {
			a.view = ViewFeeds
			a.feedToRename = nil
			a.setStatusWithKind(MsgFeedRenamed, StatusSuccess, 0)
			cmd := a.loadFeeds()
			return a, cmd
		}

	case feedDeletedMsg:
		if msg.err != nil {
			a.err = msg.err
		} else {
			a.view = ViewFeeds
			a.setStatusWithKind(MsgFeedDeleted, StatusSuccess, 0)
			a.feedToDelete = nil
			cmd := a.loadFeeds()
			return a, cmd
		}

	case refreshDoneMsg:
		// Show a concise summary in the status bar
		a.setStatus(MsgRefreshSummary(msg.updatedFeeds, msg.addedArticles, msg.errors, msg.docCount), 0)
		a.stopSpinner()

	case searchResultsMsg:
		if a.view == ViewSearch {
			a.searchResults = msg.results
			items := make([]list.Item, len(msg.results))
			for i, result := range msg.results {
				items[i] = result
			}
			a.searchList.SetItems(items)

			// Briefly show result count
			count := len(msg.results)
			if count == 0 {
				a.setStatus(MsgNoResults, 0)
			} else {
				a.setStatus(MsgResultsCount(count), 0)
			}
		}

	case searchDebounceFireMsg:
		// Only fire if this is the latest scheduled search
		if msg.seq == a.searchSeq {
			q := strings.TrimSpace(a.pendingSearchQuery)
			if len(q) > 1 {
				// Use context-aware search if we came from reader view
				if a.previousView == ViewReader && a.currentArticle != nil {
					cmds = append(cmds, a.performSearchWithContext(q, "article"))
				} else {
					cmds = append(cmds, a.performSearch(q))
				}
			}
		}

	case errorMsg:
		a.err = msg.err
		// Clear loading flag if we were loading an article
		if a.loadingArticle {
			a.loadingArticle = false
			a.stopSpinner()
		}
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
		newViewport, cmd := a.viewport.Update(msg)
		a.viewport = newViewport
		cmds = append(cmds, cmd)
	case ViewAddFeed:
		newTextInput, cmd := a.textInput.Update(msg)
		a.textInput = newTextInput
		cmds = append(cmds, cmd)
	case ViewDeleteConfirm:
	case ViewSearch:
		newSearchInput, cmd := a.searchInput.Update(msg)
		a.searchInput = newSearchInput
		cmds = append(cmds, cmd)

		newSearchList, listCmd := a.searchList.Update(msg)
		a.searchList = newSearchList
		cmds = append(cmds, listCmd)
	case ViewMedia:
		newListModel, cmd := a.mediaList.Update(msg)
		a.mediaList = newListModel
		cmds = append(cmds, cmd)
	}

	return a, tea.Batch(cmds...)
}

func (a *App) View() string {
	var content string

	switch a.view {
	case ViewFeeds:
		if len(a.feeds) == 0 {
			content = lipgloss.NewStyle().
				Width(a.width).
				Height(a.height-3).
				Align(lipgloss.Center, lipgloss.Center).
				Render(GetWelcomeMessage())
		} else {
			content = a.feedList.View()
		}
	case ViewArticles:
		content = a.articleList.View()
	case ViewReader:
		if a.loadingArticle {
			content = renderCentered(a.width, a.height-3, renderMuted(MsgLoadingArticle))
		} else {
			content = a.viewport.View()
		}
	case ViewAddFeed:
		header := renderHeader("› add feed", "Enter a feed URL and press Enter", a.width)
		inputBox := lipgloss.NewStyle().
			Width(a.width).
			Align(lipgloss.Center, lipgloss.Center).
			Render(renderInputFrame(a.textInput.View(), a.textInput.Focused(), a.width-4))
		body := lipgloss.JoinVertical(
			lipgloss.Center,
			header,
			"",
			inputBox,
			"",
			renderHelp("Press Enter to add, Esc to cancel"),
		)
		content = renderCentered(a.width, a.height-3, body)
	case ViewRenameFeed:
		// Prepare current feed name
		current := ""
		if a.feedToRename != nil {
			current = a.feedToRename.Title
			if current == "" {
				current = a.feedToRename.URL
			}
		}
		header := renderHeader("› rename feed", "Update the feed title and press Enter", a.width)
		inputBox := lipgloss.NewStyle().
			Width(a.width).
			Align(lipgloss.Center, lipgloss.Center).
			Render(renderInputFrame(a.textInput.View(), a.textInput.Focused(), a.width-4))
		body := lipgloss.JoinVertical(
			lipgloss.Center,
			header,
			"",
			inputBox,
			"",
			renderHelp("Enter: rename • Esc: cancel"),
			"",
			renderMuted("Current: "+current),
		)
		content = renderCentered(a.width, a.height-3, body)
	case ViewDeleteConfirm:
		feedName := "Unknown Feed"
		if a.feedToDelete != nil {
			feedName = a.feedToDelete.Title
			if feedName == "" {
				feedName = a.feedToDelete.URL
			}
		}

		modalWidth := (a.width * 4) / 5
		if modalWidth < 20 {
			modalWidth = a.width - 4
			if modalWidth < 15 {
				modalWidth = a.width
			}
		}

		feedName = truncateEnd(feedName, modalWidth-4)

		header := renderHeader("› delete feed", "This action cannot be undone", a.width)
		body := lipgloss.JoinVertical(
			lipgloss.Center,
			header,
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
				Width(modalWidth).
				Align(lipgloss.Center).
				Render(renderMuted("This removes all articles.")),
			"",
			"",
			renderHelp("Enter: confirm • Esc: cancel"),
		)
		content = renderCentered(a.width, a.height-3, body)
	case ViewSearch:
		searchInputWidth := a.width - 8 // Account for border, padding, and margins
		if searchInputWidth < 10 {
			searchInputWidth = a.width - 4
		}
		a.searchInput.Width = searchInputWidth

		// Build header + subtitle with engine/context
		subtitle := "global"
		if a.previousView == ViewReader && a.currentArticle != nil {
			subtitle = "in article: " + a.currentArticle.Title
		}
		if _, ok := a.searchEngine.(search.DebugStatser); ok {
			subtitle += " • full-text"
		} else {
			subtitle += " • basic"
		}
		// Truncate subtitle to fit
		subtitle = truncateEnd(subtitle, a.width-10)
		header := renderHeader("› search", subtitle, a.width)

		// Framed input
		framedInput := renderInputFrame(a.searchInput.View(), a.searchInput.Focused(), searchInputWidth)

		helpText := ""
		switch {
		case a.searchInput.Focused():
			helpText = "Type to search • Tab/↓: results • Esc: back"
		case len(a.searchList.Items()) > 0:
			helpText = "↑↓: navigate • Enter: select • Tab/↑: search box • Esc: back"
		default:
			helpText = "No results found • Tab/↑: search box • Esc: back"
		}

		searchContent := lipgloss.JoinVertical(
			lipgloss.Top,
			header,
			"",
			framedInput,
			renderMuted(helpText),
			"",
			a.searchList.View(),
		)

		content = lipgloss.NewStyle().Width(a.width).Height(a.height - 3).MaxHeight(a.height - 3).Render(searchContent)
	case ViewMedia:
		content = a.mediaList.View()
	}

	customStatus := a.getCustomStatusBar()
	separatorWidth := a.width - 2
	if separatorWidth < 0 {
		separatorWidth = 0
	}
	separator := lipgloss.NewStyle().
		Foreground(MutedColor).
		Render("─" + strings.Repeat("─", separatorWidth))

	return lipgloss.JoinVertical(lipgloss.Top, content, separator, customStatus)
}

func (a *App) getCustomStatusBar() string {
	// Highest priority: any error
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

	// Next: spinner for ongoing operations (refresh, loading article)
	if a.spinnerActive {
		left := a.statusSpinner.View()
		label := strings.TrimSpace(a.spinnerLabel)
		if label == "" {
			label = "Working…"
		}
		st := a.statusStyle(a.spinnerKind)
		msg := st.Render(left + " " + label)
		return lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 1).
			Foreground(MutedColor).
			Render(msg)
	}

	// Next: transient status message
	if a.statusText != "" && time.Now().Before(a.statusUntil) {
		st := a.statusStyle(a.statusKind)
		statusMsg := st.Render(a.statusText)
		return lipgloss.NewStyle().
			Width(a.width).
			Padding(0, 1).
			Foreground(MutedColor).
			Render(statusMsg)
	}

	commands := a.keyHandler.GetHelpForCurrentView()
	commandText := strings.Join(commands, " • ")
	if commandText == "" {
		commandText = " " // ensure status bar always renders a line
	}
	return lipgloss.NewStyle().
		Width(a.width).
		Padding(0, 1).
		Foreground(MutedColor).
		Render(commandText)
}

// setStatus shows a transient status message for the given duration.
func (a *App) setStatus(text string, d time.Duration) {
	a.setStatusWithKind(text, StatusInfo, d)
}

// setStatusWithKind shows a transient status message for the given duration and kind.
func (a *App) setStatusWithKind(text string, kind StatusKind, d time.Duration) {
	a.statusText = text
	a.statusKind = kind
	// Cap duration to 500ms by default and as a maximum
	maxDuration := 500 * time.Millisecond
	if d <= 0 || d > maxDuration {
		d = maxDuration
	}
	a.statusUntil = time.Now().Add(d)
}

// startSpinner activates the status spinner with a label and returns a Cmd to tick it.
func (a *App) startSpinner(label string) tea.Cmd {
	return a.startSpinnerWithKind(label, StatusInfo)
}

// stopSpinner deactivates the status spinner.
func (a *App) stopSpinner() {
	a.spinnerActive = false
	a.spinnerLabel = ""
}

// startSpinnerWithKind starts spinner with a severity kind.
func (a *App) startSpinnerWithKind(label string, kind StatusKind) tea.Cmd {
	a.spinnerActive = true
	a.spinnerLabel = label
	a.spinnerKind = kind
	return a.statusSpinner.Tick
}

// statusStyle returns a style for a given status kind.
func (a *App) statusStyle(kind StatusKind) lipgloss.Style {
	switch kind {
	case StatusSuccess:
		return lipgloss.NewStyle().Foreground(SuccessColor)
	case StatusWarn:
		return lipgloss.NewStyle().Foreground(UnreadColor)
	case StatusError:
		return lipgloss.NewStyle().Foreground(ErrorColor).Bold(true)
	default:
		return lipgloss.NewStyle().Foreground(MutedColor)
	}
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
	}

	return lipgloss.NewStyle().
		Foreground(SecondaryColor).
		Bold(true).
		Render("📁 " + i.feed.Title)
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
	}

	url := truncateMiddle(i.feed.URL, 80)
	return lipgloss.NewStyle().
		Foreground(MutedColor).
		Render(url)
}

func (i searchResultItem) FilterValue() string {
	if i.isArticle {
		return i.article.Title + " " + i.article.Description
	}
	return i.feed.Title + " " + i.feed.Description
}

type mediaItem struct {
	url       string
	mediaType media.Type
	index     int
	total     int
}

func (i mediaItem) Title() string {
	var typeStr string
	switch i.mediaType {
	case media.TypeVideo:
		typeStr = "🎬 Video"
	case media.TypeImage:
		typeStr = "🖼️  Image"
	case media.TypeAudio:
		typeStr = "🎵 Audio"
	case media.TypePDF:
		typeStr = "📄 PDF"
	default:
		typeStr = "Unknown"
	}
	return fmt.Sprintf("%s %d/%d", typeStr, i.index+1, i.total)
}

func (i mediaItem) Description() string {
	// Show truncated URL
	url := truncateMiddle(i.url, 80)
	return url
}

func (i mediaItem) FilterValue() string {
	return i.url
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
	err   error
	added int
	title string
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

type feedRenamedMsg struct {
	err error
}

// refreshDoneMsg summarizes a refresh operation outcome
type refreshDoneMsg struct {
	updatedFeeds  int
	addedArticles int
	errors        int
	docCount      int
}

// searchDebounceFireMsg is emitted after a short delay to trigger a debounced search.
type searchDebounceFireMsg struct {
	seq int
}
