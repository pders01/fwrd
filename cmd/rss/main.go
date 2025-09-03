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
		fmt.Println("fwrd v1.0.0")
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
	logoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FF6B6B")).
		Bold(true)

	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#95E1D3")).
		Padding(1, 2)

	logo := `  ___                     _
 / _|_      ___ __ __| |
| |_\ \ /\ / / '__/ _' |
|  _|\ V  V /| | | (_| |
|_|   \_/\_/ |_|  \__,_|`

	banner := borderStyle.Render(logoStyle.Render(logo))

	fmt.Println(lipgloss.NewStyle().
		Width(60).
		Align(lipgloss.Center).
		Render(banner))
	fmt.Println()
}
