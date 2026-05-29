# TODO - Future Improvements

This document tracks remaining improvement opportunities and optional enhancements for the fwrd RSS aggregator.

## Current Status

**Overall Test Coverage: 52.7%** (18 test files for 28 Go source files - 64.3% file coverage)

**Core modules have excellent coverage (89-90%):**
- **Validation**: 89.7% (comprehensive path, URL, and security validation tests)
- **Feed Management**: 90.2% (complete RSS/Atom parsing, fetching, and manager tests)
- **Configuration**: 89.5% (config loading, defaults, and path handling tests)
- **Debug Logging**: 82.2% (structured logging and level management tests)
- **Media**: 80.5% (type detection and launcher functionality tests)
- **Search**: 66.6% (Bleve engine and search functionality tests)
- **Storage**: 64.9% (database operations and indexing tests)

**Main testing gaps:**
- **TUI**: 25.2% (limited UI component testing - main opportunity)
- **CMD**: 16.1% (basic CLI command tests - mainly integration-tested)

---

## Recent Additions

### **Lua Scriptable Plugin System** — COMPLETED

Plugins are loaded at runtime from `~/.config/fwrd/plugins/*.lua`. No
recompile required: drop a `.lua` file, restart fwrd, and the plugin
registers as a feed-URL handler. Defaults (`reddit.lua`, `youtube.lua`)
are seeded into the plugin directory on first run via `go:embed`.

**Plugin shape:**

```lua
return {
  name = "reddit",
  priority = 50,
  can_handle = function(url) ... end,
  enhance = function(url)
    return {
      feed_url    = "...",
      title       = "...",
      description = "...",
      metadata    = { key = "value" },
    }
  end,
}
```

**Sandbox:** scripts run on gopher-lua with `string`/`table`/`math`
exposed and `io`/`os`/`package`/`debug`/`load*` removed. Host bindings:
`http.get(url, opts)`, `json.parse/encode`, `regex.match` (RE2),
`log.info/warn`. 256 KiB script-size cap; 30 s context-bound execution
matching `Manager.AddFeed`'s timeout; per-plugin mutex serialises calls
because `*lua.LState` is not goroutine-safe.

**Resilience:** a malformed plugin logs a warning and is skipped at
startup; one bad file does not block the rest of the directory.

---

### **Web View (`fwrd serve`)** — COMPLETED

A third front-end alongside the TUI and CLI, reusing the same storage,
feed, and search backends. Unlike the TUI — which degrades HTML to
terminal markdown — the web view renders article content as sanitized
HTML (the form it was authored in), so it is strictly less lossy.

**Surface (near-parity with TUI/CLI):**

- Browse feeds (with unread counts) and articles (cursor pagination)
- Read full article HTML, media links, and the original source link
- Full-text search (Bleve), front-and-center and autofocused
- Add, refresh (per-feed and all), and delete feeds
- Mark articles read/unread

**Stack:** `net/http` ServeMux (Go 1.22 method+path patterns, no router
dependency), `html/template` with `//go:embed`-ed pages and CSS. Article
HTML passes through the same `bluemonday.UGCPolicy` sanitizer the TUI
uses — the security boundary for untrusted feed content.

**Safety:** state-changing actions are no-JS `POST` forms using
Post/Redirect/Get; a same-origin guard rejects cross-site (CSRF)
submissions and caller-supplied redirects are constrained to local
paths. Index-touching mutations are serialized with a mutex because
`net/http` serves each request on its own goroutine (the
`DataListener` contract forbids concurrent notification). SIGINT/SIGTERM
trigger a graceful `http.Server.Shutdown` so BoltDB and Bleve locks
release cleanly.

**Config:** `[web] font` selects the reading font from the OS system
font library — presets `serif` (default) / `sans` / `mono`, or a raw CSS
font-family. No web fonts bundled or fetched.

**Isolation:** `--db` now relocates the search index alongside the
database, and `bleve.Open` is bounded by a timeout (`ErrIndexLocked`),
so a second instance fails loudly instead of hanging on a held lock.

---

## Optional Future Enhancements

### **Web View Enhancements**
- [ ] Star/favorite support (web + TUI; needs `Store.MarkArticleStarred`)
- [ ] Progressive-enhancement JS for inline read toggles (avoid full reload)
- [ ] OPML import/export
- [ ] Optional auth / bind guidance for non-localhost exposure
- [ ] Handler tests for add/refresh paths (currently manager-gated)

### **Testing Coverage Expansion**

#### **UI Component Testing** (Main Gap - TUI at 25.2%)
- [ ] Add tests for TUI state management and transitions
- [ ] Test keyboard navigation and input handling  
- [ ] Mock Bubble Tea program testing for view rendering
- [ ] Test error handling in UI components
- [ ] Add visual regression testing for layouts

#### **Integration Test Coverage**
- [ ] Add integration tests for complete TUI workflows
- [ ] Test error recovery scenarios more thoroughly
- [ ] Implement property-based tests for URL parsing
- [ ] End-to-end feed addition and refresh workflows
- [ ] CLI command integration tests

#### **Performance Testing**
- [ ] Add benchmark tests for search operations
- [ ] Test memory usage patterns with large datasets
- [ ] Profile concurrent refresh operations
- [ ] Benchmark RSS parsing performance
- [ ] Database operation performance testing

---

## Notes

- **All major technical debt has been resolved**
- **Core business logic is well-tested** with 89-90% coverage
- **Codebase is production-ready** with comprehensive security and validation
- **Remaining items are optional enhancements** for even higher quality
- **TUI testing is the main remaining opportunity** for coverage improvement

The application is fully functional and well-tested in all critical areas. These TODO items represent opportunities for further polish and testing completeness.