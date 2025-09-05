package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/tui"
)

// Version is the version of the application, set at build time
var Version = "dev"

func main() {
	var (
		dbPath         = flag.String("db", "", "Path to database file (overrides config)")
		configPath     = flag.String("config", "", "Path to configuration file")
		generateConfig = flag.Bool("generate-config", false, "Generate default config file")
		version        = flag.Bool("version", false, "Show version information")
		quiet          = flag.Bool("quiet", false, "Skip startup banner")
	)
	flag.Parse()

	if *version {
		fmt.Printf("fwrd %s\n", Version)
		fmt.Println("RSS aggregator")
		fmt.Println("github.com/pders01/fwrd")
		return
	}

	// Handle generate-config flag
	if *generateConfig {
		home, _ := os.UserHomeDir()
		configDir := filepath.Join(home, ".config", "fwrd")
		configFile := filepath.Join(configDir, "config.toml")

		if err := config.GenerateDefaultConfig(configFile); err != nil {
			log.Fatalf("Failed to generate config: %v", err)
		}
		fmt.Printf("Generated default configuration at: %s\n", configFile)
		return
	}

	if !*quiet {
		showBanner()
	}

	// Load configuration
	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Override database path if provided via flag
	if *dbPath != "" {
		cfg.Database.Path = *dbPath
	}

	// Expand tilde in database path
	if len(cfg.Database.Path) >= 2 && cfg.Database.Path[:2] == "~/" {
		home, _ := os.UserHomeDir()
		cfg.Database.Path = filepath.Join(home, cfg.Database.Path[2:])
	}

	store, err := storage.NewStore(cfg.Database.Path)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	app := tui.NewApp(store, cfg)
	p := tea.NewProgram(app, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
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
		"    RSS Feed Aggregator v1.0",
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
