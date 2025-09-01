package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/tui"
)

func main() {
	var (
		dbPath  = flag.String("db", "", "Path to database file")
		version = flag.Bool("version", false, "Show version information")
		quiet   = flag.Bool("quiet", false, "Skip startup banner")
	)
	flag.Parse()

	if *version {
		fmt.Println("fwrd v1.0.0")
		fmt.Println("RSS aggregator")
		fmt.Println("github.com/pders01/fwrd")
		return
	}

	if !*quiet {
		showBanner()
	}

	if *dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatal(err)
		}
		*dbPath = filepath.Join(home, ".fwrd.db")
	}

	store, err := storage.NewStore(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.Close()

	app := tui.NewApp(store)
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