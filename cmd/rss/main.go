package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/plugins"
	pluginlua "github.com/pders01/fwrd/internal/plugins/lua"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/tui"
	"github.com/pders01/fwrd/internal/validation"
	"github.com/pders01/fwrd/internal/web"
)

// stdLogger adapts standard log.Printf to plugins/lua's Logger so the
// CLI surfaces plugin load issues on stderr.
type stdLogger struct{}

func (stdLogger) Infof(format string, args ...any) { log.Printf("INFO  "+format, args...) }
func (stdLogger) Warnf(format string, args ...any) { log.Printf("WARN  "+format, args...) }

// loadLuaPlugins registers user-authored Lua plugins onto m's registry.
// Failures are logged and ignored — a malformed plugin must not break
// CLI commands that don't depend on it.
func loadLuaPlugins(m *feed.Manager) {
	dir := pluginlua.DefaultPluginDir()
	if err := pluginlua.EnsureDefaults(dir); err != nil {
		log.Printf("WARN  seeding default lua plugins in %s: %v", dir, err)
	}
	bindings := pluginlua.Bindings{
		HTTPClient: m.PluginHTTPClient(),
		Logger:     stdLogger{},
	}
	if _, err := pluginlua.LoadAndRegister(m.PluginRegistry(), dir, bindings); err != nil {
		log.Printf("WARN  loading lua plugins from %s: %v", dir, err)
	}
}

// Version is the version of the application, set at build time
var Version = "dev"

var (
	cfgFile      string
	dbPath       string
	debugFlag    bool
	quiet        bool
	forceRefresh bool
	serveAddr    string
)

var rootCmd = &cobra.Command{
	Use:   "fwrd",
	Short: "A terminal-based RSS feed aggregator",
	Long:  `fwrd is a fast, terminal-based RSS feed aggregator with full-text search capabilities.`,
	Run:   runTUI,
}

func init() {
	cobra.OnInitialize(initConfig)

	// Global flags
	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/fwrd/config.toml)")
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "", "database file path (overrides config)")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "enable debug logging to ~/.fwrd/fwrd.log")

	// TUI-specific flags
	rootCmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "skip startup banner")
	rootCmd.Flags().BoolVar(&forceRefresh, "force-refresh", false, "ignore ETag/Last-Modified headers on refresh")

	// serve flags
	serveCmd.Flags().StringVar(&serveAddr, "addr", "127.0.0.1:8080", "address to bind the web server")

	// Add commands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(feedCmd)
	rootCmd.AddCommand(pluginsCmd)
	rootCmd.AddCommand(serveCmd)
}

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Serve a read-only web view of stored feeds and articles",
	Long: `serve starts an HTTP server rendering the same feeds, articles, and
search backing the TUI. Article content is served as sanitized HTML rather
than the lossy terminal markdown the TUI must use.

The web server holds the database open for its lifetime, so it cannot run
against the same --db as a concurrent TUI (BoltDB is single-process).`,
	Run: runServe,
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Show version information",
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("fwrd %s\n", Version)
		fmt.Println("RSS aggregator")
		fmt.Println("github.com/pders01/fwrd")
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Configuration management",
}

var configGenCmd = &cobra.Command{
	Use:   "generate",
	Short: "Generate default configuration file",
	Run: func(_ *cobra.Command, _ []string) {
		home, _ := os.UserHomeDir()
		configDir := filepath.Join(home, ".config", "fwrd")
		configFile := filepath.Join(configDir, "config.toml")

		if err := config.GenerateDefaultConfig(configFile); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Printf("Generated default configuration at: %s\n", configFile)
	},
}

var feedCmd = &cobra.Command{
	Use:   "feed",
	Short: "Feed management commands",
}

var feedListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all feeds",
	Run:   listFeeds,
}

var feedAddCmd = &cobra.Command{
	Use:   "add [URL]",
	Short: "Add a new feed",
	Args:  cobra.ExactArgs(1),
	Run:   addFeed,
}

var feedDeleteCmd = &cobra.Command{
	Use:   "delete [URL or ID]",
	Short: "Delete a feed",
	Args:  cobra.ExactArgs(1),
	Run:   deleteFeed,
}

var feedRefreshCmd = &cobra.Command{
	Use:   "refresh",
	Short: "Refresh all feeds",
	Run:   refreshFeeds,
}

var pluginsCmd = &cobra.Command{
	Use:   "plugins",
	Short: "Inspect installed plugins",
}

var pluginsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List loaded Lua plugins",
	Run:   listPlugins,
}

func init() {
	configCmd.AddCommand(configGenCmd)
	feedCmd.AddCommand(feedListCmd)
	feedCmd.AddCommand(feedAddCmd)
	feedCmd.AddCommand(feedDeleteCmd)
	feedCmd.AddCommand(feedRefreshCmd)
	pluginsCmd.AddCommand(pluginsListCmd)

	// Add force flag to refresh command
	feedRefreshCmd.Flags().BoolVar(&forceRefresh, "force", false, "ignore ETag/Last-Modified headers")
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)

		viper.AddConfigPath(filepath.Join(home, ".config", "fwrd"))
		viper.AddConfigPath(".")
		viper.SetConfigName("config")
		viper.SetConfigType("toml")
	}

	viper.SetEnvPrefix("FWRD")
	viper.AutomaticEnv()
}

func loadConfig() (*config.Config, error) {
	return config.Load(cfgFile)
}

func getStore(cfg *config.Config) (*storage.Store, error) {
	// Override database path if provided via flag
	dbFilePath := cfg.Database.Path
	if dbPath != "" {
		dbFilePath = dbPath
	}

	// Use secure path handler for validation
	pathHandler := validation.NewSecurePathHandler()
	validatedPath, err := pathHandler.GetSecureDBPath(dbFilePath)
	if err != nil {
		return nil, fmt.Errorf("invalid database path: %w", err)
	}

	// Ensure the parent directory exists before opening the database
	dbDir := filepath.Dir(validatedPath)
	if _, err := pathHandler.EnsureSecureDirectory(dbDir); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	return storage.NewStoreWithTimeout(validatedPath, cfg.Database.Timeout)
}

// withStore provides consistent resource management for store operations
func withStore(fn func(*storage.Store) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := getStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer store.Close()

	return fn(store)
}

// withStoreAndConfig provides access to both store and config with proper cleanup
func withStoreAndConfig(fn func(*storage.Store, *config.Config) error) error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	store, err := getStore(cfg)
	if err != nil {
		return fmt.Errorf("failed to open store: %w", err)
	}
	defer store.Close()

	return fn(store, cfg)
}

func runTUI(_ *cobra.Command, _ []string) {
	if !quiet {
		tui.ShowBanner(Version)
	}

	// Setup debug logging if requested
	if debugFlag {
		debuglog.SetupWithBool(true)
	}

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		for _, w := range config.Warnings(cfg) {
			fmt.Fprintln(os.Stderr, "Warning:", w)
		}
		app := tui.NewApp(store, cfg)
		defer app.Close()

		// Pass force refresh option to TUI
		if forceRefresh {
			app.SetForceRefresh(true)
		}

		p := tea.NewProgram(app, tea.WithAltScreen())

		if _, err := p.Run(); err != nil {
			return fmt.Errorf("TUI error: %w", err)
		}

		return nil
	}); err != nil {
		exitWithError(err)
	}
}

// buildSearcher constructs the Bleve-backed searcher, mirroring the index
// path resolution the TUI uses. On failure it falls back to the basic
// in-memory engine so search still works, just less well.
func buildSearcher(store *storage.Store, cfg *config.Config) search.Searcher {
	idxPath := cfg.Database.SearchIndex
	if idxPath == "" {
		switch cfg.Database.Path {
		case "", storage.MemoryPath:
			idxPath = "fwrd.bleve"
		default:
			base := strings.TrimSuffix(cfg.Database.Path, filepath.Ext(cfg.Database.Path))
			idxPath = base + ".bleve"
		}
	}
	if be, err := search.NewBleveEngine(store, idxPath); err == nil && be != nil {
		return be
	}
	return search.NewEngine(store)
}

func runServe(_ *cobra.Command, _ []string) {
	if debugFlag {
		debuglog.SetupWithBool(true)
	}

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		for _, w := range config.Warnings(cfg) {
			fmt.Fprintln(os.Stderr, "Warning:", w)
		}

		searcher := buildSearcher(store, cfg)
		srv, err := web.NewServer(store, searcher, cfg)
		if err != nil {
			return fmt.Errorf("failed to build web server: %w", err)
		}

		fmt.Printf("fwrd serving on http://%s\n", serveAddr)
		if err := srv.ListenAndServe(serveAddr); err != nil {
			return fmt.Errorf("web server error: %w", err)
		}
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

// exitWithError prints err to stderr and exits non-zero. For known
// conditions (e.g. another fwrd holding the bolt lock) it adds a hint
// instead of the raw wrapped error.
func exitWithError(err error) {
	if errors.Is(err, storage.ErrDatabaseLocked) {
		fmt.Fprintln(os.Stderr, "Error: another fwrd process is already using the database.")
		fmt.Fprintln(os.Stderr, "Hint: close the other instance, or pass --db to use a different file.")
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
	os.Exit(1)
}

func listFeeds(_ *cobra.Command, _ []string) {
	if err := withStore(func(store *storage.Store) error {
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to get feeds: %w", err)
		}

		if len(feeds) == 0 {
			fmt.Println("No feeds found.")
			return nil
		}

		fmt.Printf("Found %d feeds:\n\n", len(feeds))
		for _, feed := range feeds {
			fmt.Printf("Title: %s\n", feed.Title)
			fmt.Printf("URL:   %s\n", feed.URL)
			fmt.Printf("ID:    %s\n", feed.ID)

			// Get article count
			articles, _ := store.GetArticles(feed.ID, 0)
			fmt.Printf("Articles: %d\n", len(articles))

			fmt.Printf("Last Fetched: %s\n", feed.LastFetched.Format("2006-01-02 15:04:05"))
			if feed.ETag != "" {
				fmt.Printf("ETag: %s\n", feed.ETag)
			}
			if feed.LastModified != "" {
				fmt.Printf("Last-Modified: %s\n", feed.LastModified)
			}
			fmt.Println()
		}
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func addFeed(_ *cobra.Command, args []string) {
	url := args[0]

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)

		fmt.Printf("Adding feed: %s\n", url)
		feed, err := manager.AddFeed(url)
		if err != nil {
			return fmt.Errorf("failed to add feed: %w", err)
		}

		fmt.Printf("Successfully added feed: %s (%s)\n", feed.Title, feed.URL)
		fmt.Printf("Feed ID: %s\n", feed.ID)

		// Get article count
		articles, _ := store.GetArticles(feed.ID, 0)
		fmt.Printf("Articles fetched: %d\n", len(articles))

		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func deleteFeed(_ *cobra.Command, args []string) {
	urlOrID := args[0]

	if err := withStore(func(store *storage.Store) error {
		// Find feed by URL or ID
		feeds, err := store.GetAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to get feeds: %w", err)
		}

		var targetFeed *storage.Feed
		for _, feed := range feeds {
			if feed.ID == urlOrID || feed.URL == urlOrID {
				targetFeed = feed
				break
			}
		}

		if targetFeed == nil {
			return fmt.Errorf("feed not found: %s", urlOrID)
		}

		fmt.Printf("Deleting feed: %s (%s)\n", targetFeed.Title, targetFeed.URL)

		if err := store.DeleteFeed(targetFeed.ID); err != nil {
			return fmt.Errorf("failed to delete feed: %w", err)
		}

		fmt.Println("Feed deleted successfully.")
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func listPlugins(_ *cobra.Command, _ []string) {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Error: failed to load config: %v", err)
	}
	dir := pluginlua.DefaultPluginDir()
	if seedErr := pluginlua.EnsureDefaults(dir); seedErr != nil {
		log.Printf("WARN  seeding default lua plugins in %s: %v", dir, seedErr)
	}

	reg := plugins.NewRegistry(cfg.Feed.HTTPTimeout)
	bindings := pluginlua.Bindings{Logger: stdLogger{}}
	if _, err := pluginlua.LoadAndRegister(reg, dir, bindings); err != nil {
		log.Fatalf("Error: loading plugins from %s: %v", dir, err)
	}

	loaded := reg.ListPlugins()
	fmt.Printf("Plugin directory: %s\n\n", dir)
	if len(loaded) == 0 {
		fmt.Println("No plugins loaded. Drop *.lua files into the directory and rerun.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPRIORITY\tPATH")
	for _, p := range loaded {
		path := ""
		if pp, ok := p.(interface{ Path() string }); ok {
			path = pp.Path()
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", p.Name(), p.Priority(), path)
	}
	_ = w.Flush()
}

func refreshFeeds(_ *cobra.Command, _ []string) {
	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)
		loadLuaPlugins(manager)

		// Set force refresh if requested
		if forceRefresh {
			fmt.Println("Force refresh enabled - ignoring ETag/Last-Modified headers")
			manager.SetForceRefresh(true)
		}

		fmt.Println("Refreshing all feeds...")
		summary, err := manager.RefreshAllFeeds()
		if err != nil {
			return fmt.Errorf("failed to refresh feeds: %w", err)
		}

		fmt.Printf("Refreshed %d feed(s), added %d article(s).\n",
			summary.UpdatedFeeds, summary.AddedArticles)
		return nil
	}); err != nil {
		exitWithError(err)
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
