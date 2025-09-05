# fwrd

A terminal-based RSS aggregator built with Go and Charm.sh tools. **fwrd** helps you navigate through time as expressed by new content, while being a respectful netizen that honors server resources.

## Features

- **TUI interface**: Built with Charm's Bubble Tea, Bubbles, Lip Gloss
- **Searching**: Fast search across feeds and articles
- **Polite fetching**: Honors ETag and Last-Modified; handles 304/Retry-After
- **Media integration**: Detects media and opens in appropriate apps
- **Local storage**: BoltDB-backed offline reading
- **Feed management**: Add, refresh, delete feeds; track read/unread

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

```bash
# Run with startup banner
./fwrd

# Skip banner
./fwrd -quiet

# Show version
./fwrd -version

# Load a specific config file
./fwrd -config /path/to/config.toml

# Custom database location
./fwrd -db /path/to/feeds.db

# Generate a default config (~/.config/fwrd/config.toml)
./fwrd -generate-config
```

### Keyboard Shortcuts (default)

Note: The modifier key defaults to `ctrl` and can be changed in config.

- Feeds: `ctrl+n` add • `ctrl+r` refresh • `ctrl+x` delete • `Enter` view articles
- Articles: `ctrl+m` toggle read • `Enter` read • `esc` back
- Reader: `ctrl+o` open media/links • `esc` back
- Global: `ctrl+s` search • `q` quit

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
- `github.com/mmcdole/gofeed` - RSS/Atom parsing
- `go.etcd.io/bbolt` - Embedded database

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed information about the codebase structure.

## Media players

fwrd detects media types (video, image, audio, PDF) and tries players in order configured by platform. You can customize these in the config file.

Defaults (examples):
- macOS: Video iina→mpv→vlc • Image Preview/open • Audio mpv→vlc • PDF Preview/open
- Linux: Video mpv→vlc→mplayer • Image sxiv→feh→eog/xdg-open • Audio mpv→vlc→mplayer • PDF zathura→evince→xdg-open

If no specific player is found, fwrd falls back to the platform default opener (`open`, `xdg-open`, `start`).
