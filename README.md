# fwrd

A terminal-based RSS aggregator built with Go and Charm.sh tools. **fwrd** helps you navigate through time as expressed by new content, while being a respectful netizen that honors server resources.

## Features

- **TUI Interface**: Beautiful terminal interface using Charm.sh's Bubbletea framework
- **Polite Fetching**: Respects HTTP caching headers (ETag, Last-Modified)
- **Media Integration**: 
  - Videos: Opens with iina (macOS) or mpv
  - Images: Opens with sxiv or feh
  - PDFs: Opens with system default viewer
- **Local Storage**: Uses BoltDB for offline reading
- **Feed Management**: Add, refresh, and manage multiple RSS/Atom feeds
- **Article Tracking**: Mark articles as read/unread

## Installation

### Using Go

```bash
go build -o fwrd cmd/rss/main.go
```

### Using Homebrew

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

# Custom database location
./fwrd -db /path/to/feeds.db
```

### Keyboard Shortcuts

**Feed View:**
- `a` - Add new feed
- `r` - Refresh all feeds
- `Enter` - View articles
- `q` - Quit

**Article View:**
- `m` - Mark as read/unread
- `Enter` - Read article
- `Esc` - Back to feeds
- `q` - Quit

**Reader View:**
- `o` - Open media/links
- `Esc` - Back to articles
- `q` - Quit

## Architecture

The reader is designed to be a good netizen:
- Sends proper User-Agent headers
- Honors 304 Not Modified responses
- Stores and sends If-None-Match (ETag) headers
- Stores and sends If-Modified-Since headers
- Implements rate limiting per host
- Respects Retry-After headers

## Testing

### Running Tests

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

### Integration Testing

Integration tests require Caddy web server. Install it with:

```bash
# macOS
brew install caddy

# Or use the Makefile
make install-caddy
```

Run integration tests:

```bash
cd test
./run-integration-tests.sh
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

# Create a release with GoReleaser
make release

# Create a snapshot release with GoReleaser
make release-snapshot
```

## Dependencies

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - TUI components
- `github.com/charmbracelet/lipgloss` - Styling
- `github.com/charmbracelet/glamour` - Markdown rendering
- `github.com/mmcdole/gofeed` - RSS/Atom parsing
- `go.etcd.io/bbolt` - Embedded database

## Media Players

The application will try to use the following media players:

**macOS:**
- Video: iina, mpv, vlc
- Images: sxiv, feh, Preview
- Audio: mpv, vlc

**Linux:**
- Video: mpv, vlc, mplayer
- Images: sxiv, feh, eog
- Audio: mpv, vlc, mplayer