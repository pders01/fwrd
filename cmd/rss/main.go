package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/tui"
	"github.com/pders01/fwrd/internal/validation"
)

// Version is the version of the application, set at build time
var Version = "dev"

var (
	cfgFile      string
	dbPath       string
	debugFlag    bool
	quiet        bool
	forceRefresh bool
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

	// Add commands
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(feedCmd)
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

func init() {
	configCmd.AddCommand(configGenCmd)
	feedCmd.AddCommand(feedListCmd)
	feedCmd.AddCommand(feedAddCmd)
	feedCmd.AddCommand(feedDeleteCmd)
	feedCmd.AddCommand(feedRefreshCmd)

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

	return storage.NewStore(validatedPath)
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
		showBanner()
	}

	// Setup debug logging if requested
	if debugFlag {
		debuglog.Setup(true)
	}

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		app := tui.NewApp(store, cfg)

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
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
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
		log.Fatalf("Error: %v", err)
	}
}

func addFeed(_ *cobra.Command, args []string) {
	url := args[0]

	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)

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
		log.Fatalf("Error: %v", err)
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
		log.Fatalf("Error: %v", err)
	}
}

func refreshFeeds(_ *cobra.Command, _ []string) {
	if err := withStoreAndConfig(func(store *storage.Store, cfg *config.Config) error {
		manager := feed.NewManager(store, cfg)

		// Set force refresh if requested
		if forceRefresh {
			fmt.Println("Force refresh enabled - ignoring ETag/Last-Modified headers")
			manager.SetForceRefresh(true)
		}

		fmt.Println("Refreshing all feeds...")
		if err := manager.RefreshAllFeeds(); err != nil {
			return fmt.Errorf("failed to refresh feeds: %w", err)
		}

		fmt.Println("Successfully refreshed all feeds.")
		return nil
	}); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

func showBanner() {
	// Create gradient colors for a flashier look
	colors := []lipgloss.Color{
		lipgloss.Color("#FF6B6B"),
		lipgloss.Color("#FFA86B"),
		lipgloss.Color("#95E1D3"),
		lipgloss.Color("#4ECDC4"),
		lipgloss.Color("#FF6B6B"),
	}

	// Generate ASCII art programmatically with gradient
	lines := []string{
		" ▄████ ▄     ▄▄▄▄▄▄   ▄████▄▄",
		"██▀    ██  ▄ ██   ▀██ ██   ▀██",
		"██▀▀▀▀ ██ ███ ██▀▀▀█ ██    ██",
		"██     ███████ ██   ██ ██   ██",
		"██      ██ ██  ██   ██ ███████",
		"",
	}

	// Dynamic version tagline
	versionTag := Version
	if versionTag != "" && versionTag != "dev" {
		// prefix with 'v' if not already prefixed
		if versionTag[0] != 'v' && versionTag[0] != 'V' {
			versionTag = "v" + versionTag
		}
		lines = append(lines, fmt.Sprintf("    RSS Feed Aggregator %s", versionTag))
	} else {
		lines = append(lines, "    RSS Feed Aggregator")
	}

	// Apply gradient coloring to each line
	var coloredLines []string
	for i, line := range lines {
		if line == "" {
			coloredLines = append(coloredLines, line)
			continue
		}

		// Pick color based on line index
		colorIdx := i % len(colors)
		style := lipgloss.NewStyle().
			Foreground(colors[colorIdx]).
			Bold(i < 5) // Bold for logo, normal for tagline

		coloredLines = append(coloredLines, style.Render(line))
	}

	// Create fancy border with animations-like characters
	borderChars := lipgloss.Border{
		Top:         "═",
		Bottom:      "═",
		Left:        "║",
		Right:       "║",
		TopLeft:     "╔",
		TopRight:    "╗",
		BottomLeft:  "╚",
		BottomRight: "╝",
	}

	borderStyle := lipgloss.NewStyle().
		Border(borderChars).
		BorderForeground(lipgloss.Color("#4ECDC4")).
		Padding(1, 3).
		MarginTop(1)

	// Join all lines and render with border
	banner := lipgloss.JoinVertical(lipgloss.Center, coloredLines...)
	output := borderStyle.Render(banner)

	// Center the entire banner
	fmt.Println(lipgloss.NewStyle().
		Width(70).
		Align(lipgloss.Center).
		Render(output))

	// Add a subtle separator line below
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#95E1D3")).
		Render("◆ ◇ ◆ ◇ ◆")

	fmt.Println(lipgloss.NewStyle().
		Width(70).
		Align(lipgloss.Center).
		MarginBottom(1).
		Render(separator))
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
