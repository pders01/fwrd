# fwrd

A fast, terminal-based RSS feed aggregator with full-text search capabilities, built with Go and Charm.sh tools. **fwrd** helps you navigate through time as expressed by new content, while being a respectful netizen that honors server resources.

## Features

- **Dual Interface**: Interactive TUI (Bubble Tea) + Command-line interface (Cobra)
- **Full‑text search**: Bleve‑powered search across feeds and articles with debounced input
- **Comprehensive CLI**: Complete feed management from command line (add, list, delete, refresh)
- **Smart caching**: Honors ETag and Last-Modified; handles 304/Retry-After responses
- **Security-focused**: URL validation, path sanitization, content size limits
- **Media integration**: Detects media types and opens in appropriate applications
- **Local storage**: BoltDB-backed offline reading with optimized indexing
- **Debug logging**: Structured logging system with configurable levels
- **Cross-platform**: Builds for Linux, macOS, Windows (amd64, arm64, arm)

## Installation

### Using Go

```bash
go build -o fwrd cmd/rss/main.go
```

### Using Homebrew

Install from the official tap (auto-taps on first install):

```bash
brew install pders01/fwrd/fwrd
```

### From Release Binaries

Download the appropriate binary for your platform from the [latest release](https://github.com/pders01/fwrd/releases/latest).

## Usage

### Interactive TUI Mode

```bash
# Run interactive TUI (default mode)
./fwrd

# Skip startup banner
./fwrd --quiet

# Enable debug logging
./fwrd --debug

# Custom config and database
./fwrd --config /path/to/config.toml --db /path/to/feeds.db
```

### Command Line Interface

```bash
# Show version
./fwrd version

# Generate default config
./fwrd config generate

# Feed management
./fwrd feed add "https://example.com/feed.xml"
./fwrd feed list
./fwrd feed refresh
./fwrd feed delete <feed-id>

# Get help for any command
./fwrd --help
./fwrd feed --help
```

### Keyboard Shortcuts (default)

Note: The modifier key defaults to `ctrl` and can be changed in config.

- Feeds: `ctrl+n` add • `ctrl+r` refresh • `ctrl+x` delete • `Enter` view articles
- Articles: `ctrl+m` toggle read • `Enter` read • `esc` back
- Reader: `ctrl+o` open media/links • `esc` back
- Global: `ctrl+s` search • `q` quit

### Search

- `ctrl+s` opens search. If opened from the reader view, it searches inside the current article; otherwise it searches globally across all feeds and articles. When no in‑article matches are found, fwrd automatically falls back to a global search.
- Input is debounced (~200ms) to keep the UI responsive. A short status flash shows the result count.
- Search is backed by a Bleve index by default:
  - Default DB path `~/.fwrd/fwrd.db` ⇒ index at `~/.fwrd/index.bleve`
  - Custom DB path ⇒ index sits next to the DB with a `.bleve` suffix
- The index is created on first run, re‑indexed at startup, and updated on add/refresh/delete of feeds and articles.
- To force a rebuild, remove the index directory and start fwrd again.

Status Bar

- The bottom status bar provides brief messages and a subtle spinner during long‑running actions like feed refresh or article loading.

## Architecture

The reader is designed to be a good netizen:
- Sends proper User-Agent headers
- Honors 304 Not Modified responses
- Stores and sends If-None-Match (ETag) headers
- Stores and sends If-Modified-Since headers
- Implements rate limiting per host
- Respects Retry-After headers

## Testing

### Running tests

```bash
# Run all tests
make test

# Run unit tests only
make test-unit

# Run integration tests (requires Caddy)
make test-integration

# Generate coverage report
make coverage

# Run tests with race detection
make test-race
```

Bleve index debug (optional)

```bash
# Inspect the existing on-disk index (used for debugging)
go test -tags=bleve -v -run TestDebugExistingIndex ./internal/search
```

### Integration testing

Integration tests use a local Caddy server to serve fixtures. The test suite starts Caddy automatically and waits for readiness. To run locally (requires Caddy installed):

```bash
make test-integration
```

## Building

```bash
# Build the binary
make build

# Build and run
make run

# Install to GOPATH/bin
make install

# Build Docker image
make docker-build

# Format and lint
make fmt
make lint

# Create a release with GoReleaser (CI recommended)
make release            # requires GoReleaser installed locally
make release-snapshot   # builds artifacts but does not publish
```

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - TUI components  
- `github.com/charmbracelet/lipgloss` - Styling
- `github.com/charmbracelet/glamour` - Markdown rendering
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/mmcdole/gofeed` - RSS/Atom parsing
- `go.etcd.io/bbolt` - Embedded database
- `github.com/blevesearch/bleve/v2` - Full‑text search engine
- `github.com/pelletier/go-toml/v2` - TOML configuration

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed information about the codebase structure.

Notes on Search
- The UI uses debounced input and shows brief status messages (500ms) for action feedback.
- The search engine prioritizes title/description/content (with sensible boosts) and also considers URL text.

## Media players

fwrd detects media types (video, image, audio, PDF) and tries players in order configured by platform. You can customize these in the config file.

Defaults (examples):
- macOS: Video iina→mpv→vlc • Image Preview/open • Audio mpv→vlc • PDF Preview/open
- Linux: Video mpv→vlc→mplayer • Image sxiv→feh→eog/xdg-open • Audio mpv→vlc→mplayer • PDF zathura→evince→xdg-open

If no specific player is found, fwrd falls back to the platform default opener (`open`, `xdg-open`, `start`).
