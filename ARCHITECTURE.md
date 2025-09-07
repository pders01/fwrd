# fwrd Architecture

## Overview

fwrd is a terminal-based RSS aggregator built with Go using the Charm.sh ecosystem (Bubbletea, Bubbles, Lipgloss). The application follows a modular architecture with clearly separated components.

## Components

### cmd/rss
The entry point of the application. Handles command-line arguments, initializes the application, and starts the Bubbletea program.

### internal/config
Manages application configuration from multiple sources:
- TOML config files
- Environment variables
- Command-line flags

Uses Viper for configuration management.

### internal/feed
Handles RSS/Atom feed fetching and parsing:
- Fetcher: HTTP client with caching support (ETag, Last-Modified)
- Parser: Uses gofeed library to parse RSS/Atom feeds
- Manager: Coordinates fetching and storage of feeds

### internal/media
Manages media playback and URL handling:
- Launcher: Opens media URLs with appropriate applications
- Player config: Manages media player configurations via TOML
- Media types: Detects media types from URLs and file extensions

### internal/search
Provides full‑text search functionality:
- Bleve engine (default): Robust full‑text indexing and ranking
  - Index lifecycle: created on first use, reindexed at startup from the existing DB, and incrementally updated on add/refresh/delete
  - Default index path when DB is `~/.fwrd.db`: `~/.fwrd/index.bleve`
  - For a custom DB path, the index is placed next to the DB using the same base with a `.bleve` suffix
- Basic engine fallback: A lightweight in‑memory scorer is used only if Bleve initialization fails

### internal/storage
Handles local data persistence:
- Uses BoltDB for embedded key-value storage
- Models: Struct definitions for stored data
- Store: Database operations abstraction

### internal/tui
Implements the terminal user interface:
- App: Main Bubbletea model
- Models: State models for different views (feeds, articles, reader)
- Keyhandler: Keyboard input handling
- Commands: Bubbletea commands for asynchronous operations
- Branding: UI styling and branding elements
 - Search UX: Debounced input (~200ms), brief status flashes (500ms) for feedback, in‑article search with automatic fallback to global when no matches are found

## Data Flow

1. User runs fwrd command
2. Config is loaded and validated
3. TUI is initialized with storage and config
4. Feed manager fetches and parses feeds
5. Articles are stored in BoltDB
6. User navigates feeds/articles through TUI
7. Media launcher opens URLs in external applications

## Key Features

### Polite Fetching
- Respects HTTP caching headers (ETag, Last-Modified)
- Implements rate limiting per host
- Respects Retry-After headers
- Sends proper User-Agent headers

### Cross-Platform Builds
- Uses GoReleaser for multi-platform binary generation
- Supports Linux, Windows, and macOS
- Architecture support for amd64, arm64, and arm

### Extensible Media Handling
- TOML-based player configuration
- Platform-specific player preferences
- Automatic player detection
