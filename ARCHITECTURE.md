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
