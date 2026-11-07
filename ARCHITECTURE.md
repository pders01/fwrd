# fwrd Architecture

## Overview

fwrd is a Go RSS aggregator with a Bubble Tea TUI, Cobra CLI, and HTTP web
reader. Storage, fetching, search, and editorial composition are shared across
frontends. The runtime ownership and security boundaries are described below.

## Components

### cmd/rss
The entry point of the application. Uses Cobra CLI framework to handle commands and flags, initializes configuration and storage, then either runs interactive TUI mode or executes CLI commands directly.

### internal/config
Manages application configuration from multiple sources:
- TOML config files, generated explicitly with `fwrd config generate`
- Environment variables
- Command-line flags with validation
- Path normalization and expansion at load time

Uses Viper for unified configuration management with support for config overrides and search index path customization.

### internal/control and internal/ipc
`Session` selects a local store or an RPC-backed store for an existing serve
process. `MutationCoordinator` serializes feed lifecycle operations and their
search notifications. The framed JSON protocol uses Unix sockets or Windows
named pipes, with database identity checks, cancellation, resource budgets,
and read-only transport retries.

### internal/feed
Handles RSS/Atom/JSON feed fetching and parsing:
- Fetcher: Conditional ETag/Last-Modified requests, 304 handling, configurable
  HTTP timeout, cache bypass, and a shared destination-policy transport
- Parser: Uses gofeed and derives stable identities for GUID-less articles
- Manager: Applies per-feed refresh intervals, bounds parallel bulk refreshes,
  and commits feed metadata/articles atomically before notifying listeners
- No per-host rate limiter or automatic Retry-After scheduling is implemented;
  the numeric Retry-After parsing helper is not used to schedule fetches

### internal/media
Manages media playback and URL handling:
- Launcher: Opens media URLs with appropriate applications
- Player config: Manages media player configurations via TOML
- Media types: Detects media types from URLs and file extensions

### internal/search
- **Bleve engine**: Used by local TUI sessions and serve when available. A new
  or empty index is populated from storage; a nonempty index is reused and
  updated incrementally. Startup does not rebuild every existing index
  - Index writes are batched; batch scopes have exclusive ownership
  - Default index: `~/.fwrd/index.bleve` for `~/.fwrd/fwrd.db`
  - Custom databases derive a neighboring `.bleve` path unless explicitly
    overridden through configuration
  - Search hits hydrate authoritative articles from storage
- **Basic engine**: Store-backed scoring used by IPC-connected TUIs and as an
  initialization fallback. It scans up to 200 recent articles per feed, so
  coverage and scoring differ from Bleve. Serve and mutating CLI sessions
  report index-lock errors rather than silently falling back on that error

### internal/storage
Handles high-performance local data persistence:
- **BoltDB**: Embedded key-value storage with optimized indexing strategy
- **Cursor-based pagination**: Efficient handling of large datasets
- **Optimized indexes**: Published-date index for efficient article sorting
- **Models**: Struct definitions for feeds, articles with comprehensive metadata
- **Store**: Database operations abstraction with transaction management
- **Atomic feed commits**: Subscription creation/refresh commits validators,
  timestamps, content and current read/starred flags in one bbolt transaction.
  New articles keep onboarding state; duplicate creation is idempotent
- **Field-specific mutations**: Desk assignment and title overrides update only
  existing records; refresh preserves these user-owned fields
- **Ordered indexes**: Signed seconds/nanoseconds support pre-epoch and undated
  articles. Global and per-feed cursor reads decode only the requested page.
  Existing derived indexes migrate transactionally at database open

### internal/tui
Implements the sophisticated terminal user interface:
- **App**: Main Bubbletea model with state management
- **Models**: State models for different views (feeds, articles, reader) with shared component library
- **Keyhandler**: Configurable keyboard input handling with modifier key support
- **Commands**: Bubbletea commands for asynchronous operations
- **Branding**: Centralized UI styling and modernized splash screen
- **Components**: Shared UI component library to reduce code duplication
- **Search UX**: Debounced input (~200ms), brief status flashes (500ms), in‑article search with global fallback

### internal/terminaltext and internal/service
`terminaltext.Safe` strips untrusted terminal controls before application
styling. `service` installs per-user systemd units or launchd agents, resolves
paths before installation, and quotes systemd arguments. Service installation
is loopback-only with mDNS off unless explicitly configured otherwise.

### internal/plugins
Defines the `Plugin` interface, `FeedInfo` result type, and a thread-safe
`Registry`. AddFeed consults it before fetching. Plugin callbacks execute
outside registry locks; context-aware routing supports cancellation.
`ReplacePath` and `UnregisterPath` track script ownership by file so renaming
a plugin cannot leave a stale registration behind.

### internal/plugins/lua
Implements the scriptable plugin runtime on top of gopher-lua:
- **Sandbox**: opens a whitelisted subset of the Lua stdlib
  (`string`/`table`/`math`); strips `io`/`os`/`package`/`debug` and
  metatable-mutation primitives; exposes host bindings (`http.get`,
  `json.parse`/`encode`, `regex.match`, `log.info`/`warn`)
- **Plugin**: adapts a sandboxed `*lua.LState` to the
  `plugins.Plugin` interface. Each plugin owns its own state and
  serialises calls through a context-aware admission gate because gopher-lua
  states are not goroutine-safe
- **Loader**: scans `$XDG_CONFIG_HOME/fwrd/plugins/` (falling back to
  `~/.config/fwrd/plugins/`), validates each script's returned table, and
  registers a `Plugin` per file. A 256 KiB
  script-size cap is enforced before the VM runs
- **Embed/EnsureDefaults**: ships `reddit.lua` and `youtube.lua` via
  `//go:embed` and seeds them into the plugin directory on first run
- **Watcher**: The local TUI uses fsnotify to hot-reload edits, unregister
  deletes, and retain a working plugin when a save is bad. Serve loads plugins
  once at startup; IPC clients use those server-owned plugins. Standalone
  mutating CLI sessions load plugins per invocation

### internal/editorial and internal/topics
`editorial` composes a bounded candidate set into a lead and source-balanced
stable desks, and exposes diagnostic reports. `topics` clusters article text
into cross-source developing topics used within that composition.

### internal/audit
Optional JSON-lines HTTP records cover inbound requests and shared-client
outbound traffic. URL userinfo, fragments, and query data are redacted; paths
remain visible. This is distinct from the plain-text debug logger.

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
  (cursor pagination), single article, and search. Article IDs are opaque
  composite strings derived from feed and item identities, so they ride in
  query parameters rather than path segments
- **Write handlers**: add/refresh/delete feeds, assign desks, import OPML,
  and change article read/starred state. These are `POST`-only and use Post/Redirect/Get
  (303) so a browser refresh won't re-submit
- **CSRF defense**: a same-origin guard rejects non-GET requests whose
  `Origin`/`Referer` scheme and authority don't match the request origin.
  It protects state-changing forms alongside optional HTTP Basic Auth.
  Caller-supplied redirect targets are constrained to local paths
- **Render**: `html/template` with `//go:embed`-ed templates and CSS. Article
  HTML is run through the same `bluemonday.UGCPolicy` sanitizer the TUI uses
  before being marked `template.HTML` — the security boundary for untrusted
  feed content. The web view is strictly *less* lossy than the TUI, which must
  degrade HTML to terminal markdown
- **Concurrency note**: the server is the single owner of the BoltDB file and
  Bleve index for its lifetime. A TUI or feed CLI command on the same `--db`
  connects to it over per-database local IPC; reads use an RPC-backed store and
  mutations run through the serve-side manager/coordinator. A second `serve`
  on the same database remains mutually exclusive
- **IPC reliability**: Storage and long-running feed operations use separate
  persistent connections. Idle connections remain open; handshake and partial
  frame reads are bounded. Calls include queueing/reconnection in their timeout,
  propagate request deadlines/disconnect cancellation to queued and running
  server operations, and retry read-only methods at most once after a
  transport failure. Mutations are never automatically replayed because a lost
  response does not establish whether they committed
- **TUI pagination**: Results and failures carry feed/request identities so stale
  responses cannot replace another feed's list. Failed pages retain their cursor
  and loaded items for a subsequent user-triggered retry. Search and reader
  completions carry their own generation/target identities as well; worker
  commands snapshot inputs and never mutate the live model

### Runtime ownership and security boundaries

- Local CLI/TUI and serve mutations share the coordinator ownership model.
  Listener batch scopes are exclusive from BeginBatch through CommitBatch;
  search hits hydrate authoritative storage records instead of partial articles
- App shutdown cancels commands/watchers, drains active work, and closes owned
  indexes. Renderers are exclusively leased for each render; read/star changes
  use per-article sequenced intents, applied only on the Update goroutine
- Non-loopback CLI serving requires credentials or explicit unauthenticated
  consent; service installation defaults to loopback. Configuration has one
  loader, nested environment bindings and private, atomic file replacement
- Outbound feed/plugin requests use a shared DNS-pinning transport policy,
  including redirects. Proxy environment variables are deliberately ignored;
  explicit private-network mode is a trusted-intranet exception
- Web mutation bodies and concurrent TLS classification are bounded. Generated
  certificates renew on handshake, retaining a still-valid certificate across
  transient renewal failures but never serving an expired cached certificate
- Lua loading/routing have a two-second ceiling; enhancement has a thirty-second
  ceiling and caller cancellation. JSON conversion has cycle/depth/node/byte
  budgets. This is a capability sandbox, not process-level memory isolation;
  installed plugins and native Go extensions still require trust
- Untrusted terminal text is sanitized before styling; URL credentials/query
  data are redacted from HTTP audit/error messages

### internal/validation
Provides comprehensive security validation:
- **URL Validation**: Secure feed URL validation with protocol checks, domain validation, and malicious URL detection
- **Path Sanitization**: File path security with directory traversal prevention and path normalization
- **Content Limits**: Article content size limits to prevent resource exhaustion
- **Security Policies**: Configurable validation policies (secure vs permissive modes)

### internal/debuglog
File-based diagnostic logging, separate from the CLI's user-facing output:
- **Configurable Levels**: Debug, info, warn, error logging levels
- **File Logging**: Persistent logging to `~/.fwrd/fwrd.log`, opt-in via `--debug`
- **Format**: Timestamped plain text with levels and optional `key=value`
  fields; debug logging is not JSON
- **Concurrency**: Logger writes and lifecycle changes are synchronized;
  untrusted terminal controls are removed before messages are persisted

The CLI's *operational* output (startup, plugin load, `serve` diagnostics) is
separate: it goes to stderr through `charmbracelet/log` (styled level badges,
timestamps, key=value fields, TTY-aware color), wired in `cmd/rss`. debuglog
remains the verbose file sink; the two do not overlap.

## Data Flow

### Interactive TUI Mode
1. User runs `fwrd` command
2. Cobra CLI processes flags and initializes config/logging
3. Config is loaded, validated, and paths are normalized
4. Session opens storage locally, or connects to the matching serve process
5. Local TUI owns its manager/Bleve index; a remote TUI uses store-backed search
6. Bubble Tea commands request feed operations through the local/server coordinator
7. The database owner commits articles and metadata, then notifies search
8. User navigates through TUI with real-time search
9. Media launcher opens URLs in external applications

### CLI Mode
1. User runs `fwrd [command]` (e.g., `fwrd feed add`)
2. Cobra CLI routes to appropriate command handler
3. Configuration is loaded and a local or IPC session opens the database
4. Reads use the store facade; feed mutations use coordinated local or remote operations
5. Results are output to terminal and program exits

## Key Features

### Security-First Architecture
- **URL Validation**: Submitted URLs, DNS destinations, and redirects share an explicit network policy
- **Path Sanitization**: Directory traversal protection and secure file handling
- **Content Limits**: Article size limits prevent resource exhaustion attacks
- **Input Handling**: Route-specific checks, bounded form parsing, HTML sanitization, and terminal-text sanitization

### High-Performance Storage
- **Optimized Indexing**: Published-date indexes for efficient article sorting
- **Cursor-based Pagination**: Memory-efficient handling of large datasets
- **Batched Search Indexing**: Chunked writes and exclusively owned batch scopes
- **Smart Caching**: ETag/Last-Modified support reduces bandwidth usage

### Polite Network Behavior
- **HTTP Caching**: Respects ETag and Last-Modified headers
- **Refresh Controls**: Per-feed refresh intervals and bounded bulk-fetch concurrency
- **Limitations**: No per-host limiter or automatic Retry-After backoff
- **Proper Headers**: Sends appropriate User-Agent and caching headers

### Cross-Platform Excellence
- **GoReleaser**: Multi-platform binary generation (Linux, Windows, macOS)
- **Release Targets**: Linux amd64/arm64/arm, macOS amd64/arm64, Windows amd64
- **Native Media Handling**: Platform-specific media player integration
- **TOML Configuration**: Cross-platform configuration management

### Developer-Friendly
- **Comprehensive Testing**: Extensive unit and integration test coverage
- **Debug Logging**: Structured logging with configurable levels
- **Error Handling**: Contextual error messages with proper error wrapping
- **Modular Design**: Clean separation of concerns for easy maintenance
