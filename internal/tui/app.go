package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/media"
	pluginlua "github.com/pders01/fwrd/internal/plugins/lua"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

// debugLogger adapts the package-level debuglog API to plugins/lua's
// printf-style Logger interface so plugin load failures and log.info /
// log.warn calls funnel through fwrd's existing log file.
type debugLogger struct{}

func (debugLogger) Infof(format string, args ...any) { debuglog.Infof(format, args...) }
func (debugLogger) Warnf(format string, args ...any) { debuglog.Warnf(format, args...) }

type App struct {
	config           *config.Config
	store            *storage.Store
	manager          *feed.Manager
	launcher         *media.Launcher
	searchEngine     search.Searcher
	searchEngineType string // "bleve" or "basic" - for UI display
	icons            IconSet
	keyHandler       *KeyHandler
	feedList         list.Model
	articleList      list.Model
	searchList       list.Model
	mediaList        list.Model
	searchInput      textinput.Model
	viewport         viewport.Model
	textInput        textinput.Model
	help             help.Model
	view             View
	previousView     View
	cameFromSearch   bool // Track if current article was selected from search
	feeds            []*storage.Feed
	articles         []*storage.Article
	currentFeed      *storage.Feed
	currentArticle   *storage.Article
	feedToDelete     *storage.Feed
	feedToRename     *storage.Feed
	searchResults    []searchResultItem
	mediaURLs        []string // Current media URLs being displayed
	width            int
	height           int
	err              error
	glamourRenderer  *glamour.TermRenderer
	rendererWidth    int    // Track the width used for the renderer
	themePref        string // user preference: "auto" / "light" / "dark"
	glamourStyle     string // Resolved style passed to glamour ("dark"/"light"/NoTTY)
	loadingArticle   bool   // Track if we're loading an article

	// Article list pagination state. articlesCursor stores the last
	// article ID returned by the most recent page so the next page can
	// resume from it; articlesHasMore is true while the store may still
	// have additional articles for the current feed; articlesLoadingMore
	// guards against overlapping fetches when scroll events fire faster
	// than a tea.Cmd round-trip.
	articlesCursor      string
	articlesHasMore     bool
	articlesLoadingMore bool

	// Theme change plumbing. themeEvents is signaled (without payload)
	// whenever an external source — SIGUSR1 or the macOS plist watcher —
	// asks the app to re-resolve. The reader-loop tea.Cmd installed in
	// Init waits on this channel and emits themeChangedMsg.
	themeEvents      chan struct{}
	themeWatchCancel context.CancelFunc
	themeWatchWG     sync.WaitGroup

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

	// Lua plugin hot-reload watcher; nil when no plugin dir is
	// available. shutdownOnce guards against double-Close.
	pluginWatcherCancel context.CancelFunc
	pluginWatcherWG     sync.WaitGroup
	shutdownOnce        sync.Once
}

func NewApp(store *storage.Store, cfg *config.Config) *App {
	feedList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	feedList.Title = ""
	feedList.SetShowStatusBar(false)
	feedList.SetFilteringEnabled(true)
	feedList.SetShowHelp(true) // Let Charm show native help
	// Remove title bar styling
	feedList.Styles.Title = EmptyStyle
	feedList.Styles.TitleBar = EmptyStyle

	articleList := list.New([]list.Item{}, list.NewDefaultDelegate(), 0, 0)
	articleList.Title = ""
	articleList.SetShowStatusBar(false)
	articleList.SetFilteringEnabled(true)
	articleList.SetShowHelp(true) // Let Charm show native help
	// Remove title bar styling
	articleList.Styles.Title = EmptyStyle
	articleList.Styles.TitleBar = EmptyStyle

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
		manager:  feed.NewManager(store, cfg),
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
		searchDebounceMillis: pickPositive(cfg.UI.SearchDebounceMs, config.DefaultSearchDebounceMs),
		themePref:            cfg.UI.Theme,
		glamourStyle:         resolveGlamourStyle(cfg.UI.Theme),
		themeEvents:          make(chan struct{}, 1),
		icons:                NewIconSet(cfg.UI.Icons),
	}

	// Prefer Bleve-backed engine if available (build with -tags=bleve)
	// Use search index path from config, with fallback logic for special cases
	idxPath := cfg.Database.SearchIndex
	if idxPath == "" {
		// Fallback: derive from DB path
		dbPath := cfg.Database.Path
		switch dbPath {
		case "":
			idxPath = "fwrd.bleve"
		case storage.MemoryPath:
			// Tests pass storage.MemoryPath; allocate a unique bleve
			// index path so parallel test binaries don't collide.
			idxPath = filepath.Join(os.TempDir(), fmt.Sprintf("fwrd-index-%d.bleve", time.Now().UnixNano()))
		default:
			base := strings.TrimSuffix(dbPath, filepath.Ext(dbPath))
			idxPath = base + ".bleve"
		}
	}
	// Initialize search engine with fallback strategy
	debuglog.Infof("Initializing search engine with index path: %s", idxPath)
	if be, err := search.NewBleveEngine(store, idxPath); err == nil && be != nil {
		app.searchEngine = be
		app.searchEngineType = "bleve"
		debuglog.Infof("Successfully initialized Bleve search engine")
	} else {
		debuglog.Errorf("Bleve search engine initialization failed: %v", err)
		debuglog.Infof("Falling back to basic search engine")
		app.searchEngine = search.NewEngine(store)
		app.searchEngineType = "basic"
	}

	// Wire the search engine into the manager so it receives index updates
	// after every successful add/refresh without the TUI re-implementing the
	// dispatch.
	if dl, ok := app.searchEngine.(feed.DataListener); ok {
		app.manager.RegisterDataListener(dl)
	}
	if bs, ok := app.searchEngine.(feed.BatchScope); ok {
		app.manager.RegisterBatchScope(bs)
	}

	pluginDir := pluginlua.DefaultPluginDir()
	if err := pluginlua.EnsureDefaults(pluginDir); err != nil {
		debuglog.Errorf("seeding default lua plugins in %s: %v", pluginDir, err)
	}
	bindings := pluginlua.Bindings{
		HTTPClient: app.manager.PluginHTTPClient(),
		Logger:     debugLogger{},
	}
	if n, err := pluginlua.LoadAndRegister(app.manager.PluginRegistry(), pluginDir, bindings); err != nil {
		debuglog.Errorf("loading lua plugins from %s: %v", pluginDir, err)
	} else if n > 0 {
		debuglog.Infof("loaded %d lua plugin(s) from %s", n, pluginDir)
	}

	if pluginDir != "" {
		if watcher, err := pluginlua.NewWatcher(app.manager.PluginRegistry(), pluginDir, bindings); err != nil {
			debuglog.Warnf("plugin hot-reload disabled: %v", err)
		} else {
			ctx, cancel := context.WithCancel(context.Background())
			app.pluginWatcherCancel = cancel
			app.pluginWatcherWG.Add(1)
			go func() {
				defer app.pluginWatcherWG.Done()
				if rerr := watcher.Run(ctx); rerr != nil && !errors.Is(rerr, context.Canceled) {
					debuglog.Warnf("plugin watcher exited: %v", rerr)
				}
			}()
		}
	}

	app.keyHandler = NewKeyHandler(app, cfg)

	// Initialize status spinner (subtle)
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = StatusInfoStyle
	app.statusSpinner = sp

	return app
}

// SetForceRefresh configures the fetcher to ignore ETag/Last-Modified headers
func (a *App) SetForceRefresh(force bool) {
	if a.manager != nil {
		a.manager.SetForceRefresh(force)
	}
}

// Close releases App-owned resources that outlive the Bubble Tea
// program loop — currently the plugin hot-reload watcher. Safe to call
// multiple times.
func (a *App) Close() {
	a.shutdownOnce.Do(func() {
		if a.pluginWatcherCancel != nil {
			a.pluginWatcherCancel()
		}
		a.pluginWatcherWG.Wait()
		if a.themeWatchCancel != nil {
			a.themeWatchCancel()
		}
		a.themeWatchWG.Wait()
	})
}

// applyResolvedStyle re-resolves the glamour style from the current
// preference and invalidates the renderer cache so the next render
// rebuilds with the new style. Returns true when the style actually
// changed.
func (a *App) applyResolvedStyle() bool {
	next := resolveGlamourStyle(a.themePref)
	if next == a.glamourStyle {
		return false
	}
	a.glamourStyle = next
	a.glamourRenderer = nil
	return true
}

// signalThemeChange wakes the watcher reader without blocking. The
// channel is buffered to one slot so coalesced bursts (e.g. a flurry
// of plist writes) collapse into a single re-resolve.
func (a *App) signalThemeChange() {
	if a.themeEvents == nil {
		return
	}
	select {
	case a.themeEvents <- struct{}{}:
	default:
	}
}

// waitThemeChange returns a tea.Cmd that blocks on the next theme
// event and emits themeChangedMsg. Update re-issues this command after
// each event so the watcher behaves like a long-lived subscription.
func (a *App) waitThemeChange() tea.Cmd {
	return func() tea.Msg {
		_, ok := <-a.themeEvents
		if !ok {
			return nil
		}
		return themeChangedMsg{}
	}
}

// themeChangedMsg is dispatched by waitThemeChange when an external
// signal source (SIGUSR1, macOS plist watcher) has fired.
type themeChangedMsg struct{}

func (a *App) getRenderer() (*glamour.TermRenderer, error) {
	wordWrapWidth := max(
		// maximum for readability
		min((a.width*9)/10,

			MaxReadableWidth),
		// minimum for readability
		MinReadableWidth)
	if a.width < NarrowScreenThreshold {
		wordWrapWidth = max(getContentWidth(a.width), MinNarrowWidth)
	}

	if a.glamourRenderer == nil || abs(a.rendererWidth-wordWrapWidth) > RendererWidthTolerance {
		r, err := glamour.NewTermRenderer(
			glamour.WithStandardStyle(a.glamourStyle),
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
	a.startThemeWatchers()
	return tea.Batch(
		a.loadFeeds(),
		tea.EnterAltScreen,
		a.waitThemeChange(),
	)
}

// startThemeWatchers spawns SIGUSR1 and (on macOS) plist-based theme
// observers. Both write to a.themeEvents. Cancelling the context shuts
// them down via Close.
func (a *App) startThemeWatchers() {
	ctx, cancel := context.WithCancel(context.Background())
	a.themeWatchCancel = cancel

	a.themeWatchWG.Add(1)
	go func() {
		defer a.themeWatchWG.Done()
		watchThemeSignal(ctx, a.signalThemeChange)
	}()

	if err := watchSystemTheme(ctx, &a.themeWatchWG, a.signalThemeChange); err != nil {
		debuglog.Warnf("system theme watcher disabled: %v", err)
	}
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
		a.feedList.SetSize(msg.Width, msg.Height-listViewChrome)
		a.articleList.SetSize(msg.Width, msg.Height-listViewChrome)
		searchListHeight := max(msg.Height-searchViewChrome, minSearchListHeight)
		a.searchList.SetSize(msg.Width, searchListHeight)
		a.mediaList.SetSize(msg.Width, msg.Height-viewportChrome)
		a.viewport.Width = msg.Width
		a.viewport.Height = msg.Height - viewportChrome

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
			if msg.appendPage {
				a.articles = append(a.articles, msg.articles...)
				items := a.articleList.Items()
				for _, art := range msg.articles {
					items = append(items, articleItem{article: art, maxDescLen: a.config.UI.Article.MaxDescriptionLength})
				}
				a.articleList.SetItems(items)
			} else {
				a.articles = msg.articles
				items := make([]list.Item, len(msg.articles))
				for i, art := range msg.articles {
					items[i] = articleItem{article: art, maxDescLen: a.config.UI.Article.MaxDescriptionLength}
				}
				a.articleList.SetItems(items)
			}
			a.articlesCursor = msg.cursor
			a.articlesHasMore = msg.hasMore
			a.articlesLoadingMore = false
		}

	case articleReadToggledMsg:
		if msg.err != nil {
			a.err = msg.err
		} else if msg.article != nil {
			msg.article.Read = msg.read
		}

	case articleRenderedMsg:
		// Always clear loading state — the render finished, regardless of
		// whether the user has since navigated away. Setting viewport content
		// when not in ViewReader is harmless (it's just buffered for next entry).
		a.viewport.SetContent(msg.content)
		a.viewport.GotoTop()
		a.loadingArticle = false
		a.stopSpinner()

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

	case themeChangedMsg:
		// Re-resolve from current preference; on a real change rebuild
		// the renderer cache and re-render the current article so the
		// reader updates without user interaction. Always re-arm the
		// watcher command — it's a long-lived subscription.
		if a.applyResolvedStyle() {
			a.setStatusWithKind(MsgThemeApplied(a.themePref, a.glamourStyle), StatusInfo, 2*time.Second)
			if a.view == ViewReader && a.currentArticle != nil {
				cmds = append(cmds, a.renderArticle(a.currentArticle))
			}
		}
		cmds = append(cmds, a.waitThemeChange())
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
		if more := a.maybeLoadMoreArticles(); more != nil {
			cmds = append(cmds, more)
		}
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
			content = renderCentered(a.width, a.height-3, GetWelcomeMessage())
		} else {
			header := renderHeader("› feeds", "", a.width)
			content = lipgloss.JoinVertical(lipgloss.Top, header, a.feedList.View())
		}
	case ViewArticles:
		subtitle := ""
		if a.currentFeed != nil {
			// Show feed title or URL as subtitle, truncated
			st := a.currentFeed.Title
			if st == "" {
				st = a.currentFeed.URL
			}
			subtitle = truncateForSubtitle(st, a.width)
		}
		header := renderHeader("› articles", subtitle, a.width)
		content = lipgloss.JoinVertical(lipgloss.Top, header, a.articleList.View())
	case ViewReader:
		if a.loadingArticle {
			content = renderCentered(a.width, a.height-3, renderMuted(MsgLoadingArticle))
		} else {
			content = a.viewport.View()
		}
	case ViewAddFeed:
		header := renderHeader("› add feed", "Enter a feed URL and press Enter", a.width)
		inputBox := renderInputFrame(a.textInput.View(), a.textInput.Focused(), a.width-4)
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
		inputBox := renderInputFrame(a.textInput.View(), a.textInput.Focused(), a.width-4)
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
		if modalWidth < MinNarrowWidth {
			modalWidth = getModalWidth(a.width)
			if modalWidth < MinModalWidth {
				modalWidth = a.width
			}
		}

		feedName = truncateForModal(feedName, modalWidth)

		header := renderHeader("› delete feed", "This action cannot be undone", a.width)
		body := lipgloss.JoinVertical(
			lipgloss.Center,
			header,
			"",
			renderModalQuestion("Delete this feed?", modalWidth),
			"",
			renderModalHighlight(feedName, modalWidth),
			"",
			renderModalInfo(renderMuted("This removes all articles."), modalWidth),
			"",
			"",
			renderHelp("Enter: confirm • Esc: cancel"),
		)
		content = renderCentered(a.width, a.height-3, body)
	case ViewSearch:
		a.searchInput.Width = getInputWidth(a.width)

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
		subtitle = truncateForSubtitle(subtitle, a.width)
		header := renderHeader("› search", subtitle, a.width)

		// Framed input
		framedInput := renderInputFrame(a.searchInput.View(), a.searchInput.Focused(), a.searchInput.Width)

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

		content = ContentWrapper(a.width, a.height-3).Render(searchContent)
	case ViewMedia:
		content = a.mediaList.View()
	}

	customStatus := a.getCustomStatusBar()
	separatorWidth := max(getSeparatorWidth(a.width), 0)
	separator := SeparatorStyle.Render("─" + strings.Repeat("─", separatorWidth))

	return lipgloss.JoinVertical(lipgloss.Top, content, separator, customStatus)
}

func (a *App) getCustomStatusBar() string {
	// Highest priority: any error
	if a.err != nil {
		errorMsg := ErrorMessageStyle.Render(fmt.Sprintf("%s %v", a.icons.Error, a.err))

		return StatusBarStyleWithPadding().
			Width(a.width).
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
		return StatusBarStyleWithPadding().
			Width(a.width).
			Render(msg)
	}

	// Next: transient status message
	if a.statusText != "" && time.Now().Before(a.statusUntil) {
		st := a.statusStyle(a.statusKind)
		statusMsg := st.Render(a.statusText)
		return StatusBarStyleWithPadding().
			Width(a.width).
			Render(statusMsg)
	}

	commands := a.keyHandler.GetHelpForCurrentView()
	commandText := strings.Join(commands, " • ")
	if commandText == "" {
		commandText = " " // ensure status bar always renders a line
	}
	return StatusBarStyleWithPadding().
		Width(a.width).
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
	// Cap duration to DefaultStatusDuration by default and as a maximum
	maxDuration := DefaultStatusDuration
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
		return StatusSuccessStyle
	case StatusWarn:
		return StatusWarnStyle
	case StatusError:
		return StatusErrorStyle
	default:
		return StatusInfoStyle
	}
}

// getSearchEngineStatus returns a formatted string showing the search engine type and status.
func (a *App) getSearchEngineStatus() string {
	prefix := ""
	if a.icons.Search != "" {
		prefix = a.icons.Search + " "
	}
	switch a.searchEngineType {
	case "bleve":
		return StatusSuccessStyle.Render(prefix + "bleve")
	case "basic":
		return StatusWarnStyle.Render(prefix + "basic")
	default:
		return StatusInfoStyle.Render(prefix + "unknown")
	}
}

type feedItem struct {
	feed *storage.Feed
}

func (i feedItem) Title() string       { return i.feed.Title }
func (i feedItem) Description() string { return i.feed.Description }
func (i feedItem) FilterValue() string { return i.feed.Title }

type articleItem struct {
	article    *storage.Article
	maxDescLen int
}

func (i articleItem) Title() string {
	if i.article.Read {
		return ReadItemStyle.Render(i.article.Title)
	}
	return UnreadItemStyle.Render("● " + i.article.Title)
}

func (i articleItem) Description() string {
	desc := i.article.Description
	limit := i.maxDescLen
	if limit <= 0 {
		limit = defaultMaxDescriptionLength
	}
	if len(desc) > limit {
		desc = desc[:limit] + "…"
	}

	timeStr := ""
	if !i.article.Published.IsZero() {
		timeStr = TimeStyle.Render(" • " + i.article.Published.Format("Jan 2, 15:04"))
	}

	return renderMuted(desc) + timeStr
}

func (i articleItem) FilterValue() string { return i.article.Title }

type searchResultItem struct {
	feed      *storage.Feed
	article   *storage.Article
	icons     *IconSet
	isArticle bool
}

func (i searchResultItem) Title() string {
	icons := i.iconSet()
	if i.isArticle {
		if i.article.Read {
			return ReadItemStyle.Render(withIcon(icons.Article, i.article.Title))
		}
		marker := icons.Article
		if marker == "" {
			marker = icons.Unread
		}
		return UnreadItemStyle.Render(withIcon(marker, i.article.Title))
	}

	return FeedTitleStyle.Render(withIcon(icons.Feed, i.feed.Title))
}

func (i searchResultItem) iconSet() IconSet {
	if i.icons != nil {
		return *i.icons
	}
	return unicodeIcons
}

func (i searchResultItem) Description() string {
	if i.isArticle {
		desc := i.article.Description
		if len(desc) > searchResultDescLength {
			desc = desc[:searchResultDescLength] + "…"
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

		return renderMuted(desc + " • from " + feedName + " • " + timeStr)
	}

	url := truncateMiddle(i.feed.URL, 80)
	return renderMuted(url)
}

func (i searchResultItem) FilterValue() string {
	if i.isArticle {
		return i.article.Title + " " + i.article.Description
	}
	return i.feed.Title + " " + i.feed.Description
}

type mediaItem struct {
	url       string
	icons     *IconSet
	mediaType media.Type
	index     int
	total     int
	// isArticle marks the synthetic "open article" entry that
	// openMediaList prepends so users can still reach the parent
	// article's canonical URL even when the article carries multiple
	// media URLs. The entry is not part of currentArticle.MediaURLs.
	isArticle bool
}

func (i mediaItem) Title() string {
	icons := unicodeIcons
	if i.icons != nil {
		icons = *i.icons
	}
	if i.isArticle {
		return withIcon(icons.Article, "Open article")
	}
	var typeStr string
	switch i.mediaType {
	case media.TypeVideo:
		typeStr = withIcon(icons.Video, "Video")
	case media.TypeImage:
		typeStr = withIcon(icons.Image, "Image")
	case media.TypeAudio:
		typeStr = withIcon(icons.Audio, "Audio")
	case media.TypePDF:
		typeStr = withIcon(icons.PDF, "PDF")
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
	articles   []*storage.Article
	cursor     string
	appendPage bool
	hasMore    bool
}

// articleReadToggledMsg reports the result of an in-place read-state
// flip. The handler mutates the article's Read field on the Update
// goroutine; the list re-reads it on the next render frame so no
// SetItem call is needed and the user's selection / scroll position
// stays put — unlike a full loadArticles reload which resets both.
type articleReadToggledMsg struct {
	article *storage.Article
	err     error
	read    bool
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
