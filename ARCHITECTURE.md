# fwrd Architecture

## Overview

fwrd is a fast, terminal-based RSS aggregator built with Go using both interactive TUI (Charm.sh ecosystem) and CLI interfaces (Cobra). The application follows a modular, security-focused architecture with clearly separated components and comprehensive validation systems.

## Components

### cmd/rss
The entry point of the application. Uses Cobra CLI framework to handle commands and flags, initializes configuration and storage, then either runs interactive TUI mode or executes CLI commands directly.

### internal/config
Manages application configuration from multiple sources:
- TOML config files with automatic generation
- Environment variables
- Command-line flags with validation
- Path normalization and expansion at load time

Uses Viper for unified configuration management with support for config overrides and search index path customization.

### internal/feed
Handles RSS/Atom feed fetching and parsing with comprehensive validation:
- Fetcher: HTTP client with smart caching (ETag, Last-Modified), rate limiting, and force-refresh support
- Parser: Uses gofeed library with content size limits for security
- Manager: Coordinates fetching, parsing, validation, and storage with error handling

### internal/media
Manages media playback and URL handling:
- Launcher: Opens media URLs with appropriate applications
- Player config: Manages media player configurations via TOML
- Media types: Detects media types from URLs and file extensions

### internal/search
Provides high-performance full‑text search functionality:
- **Bleve engine** (default): Robust full‑text indexing with streaming/chunked processing to prevent OOM
  - Index lifecycle: created on first use, reindexed at startup from existing DB, incrementally updated
  - Smart batching: Uses streaming indexing for large datasets with memory management
  - Default index path when DB is `~/.fwrd/fwrd.db`: `~/.fwrd/index.bleve`
  - For custom DB path, index is placed next to DB with `.bleve` suffix
  - Configurable index path override via config
- **Basic engine fallback**: Lightweight in‑memory scorer used only if Bleve initialization fails

### internal/storage
Handles high-performance local data persistence:
- **BoltDB**: Embedded key-value storage with optimized indexing strategy
- **Cursor-based pagination**: Efficient handling of large datasets
- **Optimized indexes**: Published-date index for efficient article sorting
- **Models**: Struct definitions for feeds, articles with comprehensive metadata
- **Store**: Database operations abstraction with transaction management

### internal/tui
Implements the sophisticated terminal user interface:
- **App**: Main Bubbletea model with state management
- **Models**: State models for different views (feeds, articles, reader) with shared component library
- **Keyhandler**: Configurable keyboard input handling with modifier key support
- **Commands**: Bubbletea commands for asynchronous operations
- **Branding**: Centralized UI styling and modernized splash screen
- **Components**: Shared UI component library to reduce code duplication
- **Search UX**: Debounced input (~200ms), brief status flashes (500ms), in‑article search with global fallback

### internal/plugins
Defines the `Plugin` interface, `FeedInfo` result type, and a thread-safe
`Registry`. The registry is consulted by `feed.Manager.AddFeed` to enhance
a URL before fetch — for example, turning a subreddit listing URL into
its `.rss` endpoint. `Registry.Replace` and `Registry.Unregister`
support runtime plugin churn (see `internal/plugins/lua` below).

### internal/plugins/lua
Implements the scriptable plugin runtime on top of gopher-lua:
- **Sandbox**: opens a whitelisted subset of the Lua stdlib
  (`string`/`table`/`math`); strips `io`/`os`/`package`/`debug` and
  metatable-mutation primitives; exposes host bindings (`http.get`,
  `json.parse`/`encode`, `regex.match`, `log.info`/`warn`)
- **Plugin**: adapts a sandboxed `*lua.LState` to the
  `plugins.Plugin` interface. Each plugin owns its own state and
  serialises calls through a mutex because gopher-lua states are not
  goroutine-safe
- **Loader**: scans `~/.config/fwrd/plugins/`, validates each script's
  returned table, and registers a `Plugin` per file. A 256 KiB
  script-size cap is enforced before the VM runs
- **Embed/EnsureDefaults**: ships `reddit.lua` and `youtube.lua` via
  `//go:embed` and seeds them into the plugin directory on first run
- **Watcher**: uses fsnotify to hot-reload edits, unregister deletes,
  and keep a previously-working plugin in place when a save is bad

### internal/web
An HTTP front-end over the same storage, feed, and search backends the TUI
and CLI use — a third consumer of the interface-agnostic core, not a fork of
it. It reaches near-parity with the other front-ends:
- **Server**: `net/http` ServeMux (Go 1.22 method+path patterns, no router
  dependency) with hardened timeouts; constructed from the same `*storage.Store`,
  `*feed.Manager`, and `search.Searcher` `runTUI` builds. The manager is wired
  with the searcher as a `DataListener`/`BatchScope` so feeds added or refreshed
  via the web are indexed for search, exactly as in the TUI
- **Read handlers**: feed list (with unread counts), per-feed article list
  (cursor pagination), single article, and search. Article IDs are composite
  (`feedID:articleURL`) so they ride in a query parameter rather than a path
  segment
- **Write handlers**: add / refresh (per-feed and all) / delete feeds, and
  mark articles read/unread. These are `POST`-only and use Post/Redirect/Get
  (303) so a browser refresh won't re-submit
- **CSRF defense**: a same-origin guard rejects non-GET requests whose
  `Origin`/`Referer` host doesn't match the request host. With no cookies or
  sessions there's no credential to steal, but the guard still blocks forged
  cross-site form posts (e.g. a feed-delete). Caller-supplied redirect targets
  are constrained to local paths to prevent open redirects
- **Render**: `html/template` with `//go:embed`-ed templates and CSS. Article
  HTML is run through the same `bluemonday.UGCPolicy` sanitizer the TUI uses
  before being marked `template.HTML` — the security boundary for untrusted
  feed content. The web view is strictly *less* lossy than the TUI, which must
  degrade HTML to terminal markdown
- **Concurrency note**: the server holds the BoltDB file and the Bleve index
  open for its lifetime, so `serve` is mutually exclusive with a TUI or second
  `serve` on the same `--db`/index

### internal/validation
Provides comprehensive security validation:
- **URL Validation**: Secure feed URL validation with protocol checks, domain validation, and malicious URL detection
- **Path Sanitization**: File path security with directory traversal prevention and path normalization
- **Content Limits**: Article content size limits to prevent resource exhaustion
- **Security Policies**: Configurable validation policies (secure vs permissive modes)

### internal/debuglog
Structured logging system for debugging and monitoring:
- **Configurable Levels**: Debug, info, warn, error logging levels
- **File Logging**: Persistent logging to `~/.fwrd/fwrd.log`
- **Structured Output**: JSON-formatted logs for machine processing
- **Performance Monitoring**: Operation timing and resource usage tracking

## Data Flow

### Interactive TUI Mode
1. User runs `fwrd` command
2. Cobra CLI processes flags and initializes config/logging
3. Config is loaded, validated, and paths are normalized
4. Database and search index are initialized
5. TUI is started with Bubbletea framework
6. Feed manager fetches/parses feeds with validation
7. Articles are stored in BoltDB and indexed for search
8. User navigates through TUI with real-time search
9. Media launcher opens URLs in external applications

### CLI Mode
1. User runs `fwrd [command]` (e.g., `fwrd feed add`)
2. Cobra CLI routes to appropriate command handler
3. Config and database are initialized
4. Command executes directly (add feed, list feeds, etc.)
5. Results are output to terminal and program exits

## Key Features

### Security-First Architecture
- **URL Validation**: Comprehensive validation prevents malicious feed URLs
- **Path Sanitization**: Directory traversal protection and secure file handling
- **Content Limits**: Article size limits prevent resource exhaustion attacks
- **Input Validation**: All user inputs are validated and sanitized

### High-Performance Storage
- **Optimized Indexing**: Published-date indexes for efficient article sorting
- **Cursor-based Pagination**: Memory-efficient handling of large datasets
- **Streaming Search Indexing**: Chunked processing prevents OOM on large feeds
- **Smart Caching**: ETag/Last-Modified support reduces bandwidth usage

### Polite Network Behavior
- **HTTP Caching**: Respects ETag and Last-Modified headers
- **Rate Limiting**: Per-host rate limiting prevents server overload
- **Retry-After**: Honors server retry requests
- **Proper Headers**: Sends appropriate User-Agent and caching headers

### Cross-Platform Excellence
- **GoReleaser**: Multi-platform binary generation (Linux, Windows, macOS)
- **Architecture Support**: amd64, arm64, and arm architectures
- **Native Media Handling**: Platform-specific media player integration
- **TOML Configuration**: Cross-platform configuration management

### Developer-Friendly
- **Comprehensive Testing**: Extensive unit and integration test coverage
- **Debug Logging**: Structured logging with configurable levels
- **Error Handling**: Contextual error messages with proper error wrapping
- **Modular Design**: Clean separation of concerns for easy maintenance
