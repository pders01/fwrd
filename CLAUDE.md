# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**fwrd** is a terminal-based RSS aggregator built with Go and Charm.sh tools. It's designed to be a respectful netizen that honors server resources through proper HTTP caching.

## Development Commands

### Build and Run
```bash
make build          # Build the binary
make run           # Build and run
make install       # Install to GOPATH/bin
```

### Testing
```bash
make test          # Run all tests (unit + integration)
make test-unit     # Run unit tests only
make test-integration  # Run integration tests (requires Caddy)
make test-race     # Run tests with race detection
make coverage      # Generate coverage report
```

### Code Quality
```bash
make lint          # Run linters (golangci-lint or go vet)
make fmt           # Format code with gofmt
```

### Running a Single Test
```bash
# Run specific test function
go test -run TestFunctionName ./internal/feed/

# Run tests in specific package
go test ./internal/storage/

# Run with verbose output
go test -v ./internal/tui/
```

## Architecture

### Core Components

- **cmd/rss/main.go**: Entry point, handles CLI flags and initializes the TUI
- **internal/tui/**: Terminal UI using Bubbletea framework
  - `app.go`: Main application state and message handling
  - `keyhandler.go`: Keyboard input processing
  - `models.go`: View state management (FeedView, ArticleView, ReaderView)
- **internal/feed/**: RSS/Atom feed handling
  - `fetcher.go`: HTTP fetching with caching (ETag, Last-Modified)
  - `parser.go`: Feed parsing using gofeed
  - `manager.go`: Feed refresh orchestration
- **internal/storage/**: BoltDB persistence layer
  - `store.go`: Database operations for feeds and articles
  - `models.go`: Data models (Feed, Article, FetchMetadata)
- **internal/media/**: Media file launching
  - `launcher.go`: Opens videos/images/PDFs with appropriate applications
- **internal/search/**: Article search functionality
  - `engine.go`: Full-text search implementation

### Key Design Patterns

1. **Message-Based Architecture**: Uses Bubbletea's message pattern for state updates
2. **Polite Fetching**: Respects HTTP caching headers, implements rate limiting
3. **View State Machine**: Three main views (Feed, Article, Reader) with clear transitions
4. **Embedded Database**: Uses BoltDB for offline-first storage

### Integration Testing

Integration tests require a local Caddy server to serve test fixtures. The test setup:
1. Starts Caddy with test/fixtures/Caddyfile
2. Serves sample RSS/Atom feeds from test/fixtures/
3. Tests actual HTTP fetching with caching behavior

## Important Notes

- Always honor HTTP caching headers when modifying the fetcher
- The TUI uses Charm.sh components - follow their patterns for consistency
- Media launcher falls back through multiple players per platform
- Database path defaults to ~/.fwrd.db but can be overridden with -db flag