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

### **Port 80 (`fwrd net`), responsive web UI, fail-fast serve, `fwrd logs`** — COMPLETED

A round of usability fixes from real-world use:

- **`fwrd net up/down/status` (`internal/netbind`)** — expose the web view at a
  bare `http://fwrd.local` (port 80) without binding a privileged port and
  without colliding with a host process already on :80. It gives fwrd a
  dedicated LAN alias IP and installs a PREROUTING/rdr redirect from that IP's
  :80 to fwrd's unprivileged port — `pf` on macOS (loads into the
  `com.apple/fwrd` sub-anchor that the stock `rdr-anchor "com.apple/*"` already
  evaluates, so `/etc/pf.conf` is never touched), `nftables` on Linux (own
  `fwrd` table). The redirect precedes the socket lookup, so it works even when the
  host binds `0.0.0.0:80`. mDNS pins `fwrd.local` to the alias IP only via
  `mdns.AdvertiseOn` + `serve --mdns-ip`. Root-only; state in `~/.fwrd/net.json`;
  not reboot-persistent (documented). Platform-split with an unsupported stub;
  pure render/state functions tested.
- **Responsive web layout** — added a `@media (max-width: 40rem)` block: the
  masthead search drops to a full-width line, the two-up add-feed/sort toolbars
  collapse to one column, and `.page-head` wraps so action buttons never get
  pushed off-screen.
- **Centered reader column** — the article reader now narrows its whole `<main>`
  (`main.reading`, `--readw`) so crumbs + headline + body share one centered
  column under the full-width masthead, instead of hugging the left edge.
- **Fail-fast serve** — `serve` binds the port *before* logging "serving" or
  advertising mDNS, so an in-use port fails immediately with a clear hint
  (`web.Server.Listen`/`Serve` split) rather than a misleading "serving" line.
  The systemd unit gained `StartLimitIntervalSec`/`StartLimitBurst` so a
  persistent bind failure surfaces as a `failed` unit instead of an infinite
  restart loop.
- **`fwrd logs`** — convenience wrapper: tails `~/.fwrd/fwrd.log` by default, or
  the background service's output with `--service` (journalctl on Linux, the
  LaunchAgent's `serve.*.log` on macOS); `-f`/`-n` for follow/line-count.

Code: `internal/netbind/`, `internal/mdns/mdns.go`, `internal/web/server.go`,
`internal/web/templates/{style.css,article.html}`, `internal/service/`,
`internal/debuglog/log.go`, `cmd/rss/main.go`.



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

### **Web View Enhancements** — COMPLETED

The optional web-view follow-ups are now implemented:

- **Star/favorite** — `Store.MarkArticleStarred` backs a `★` toggle in the
  web view (feed list + article) and the TUI (`ctrl+f` in the article list
  and reader, configurable via `[keys].toggle_star`). State is shared across
  all three front-ends.
- **Progressive-enhancement JS** — an embedded `app.js` upgrades the
  read/star toggle forms to update in place via `fetch` (`redirect:
  "manual"`, so the 303 is not followed into a wasted page render). With JS
  off, the same no-JS POST forms drive the change — the server stays the
  single source of truth.
- **OPML import/export** — a shared `internal/opml` package (OPML 2.0,
  nested-outline aware, dedup on import) is exposed both on the CLI
  (`fwrd feed export/import [path]`, `-` for stdio) and the web view
  (`GET /opml/export` download, `POST /opml/import` upload, 2 MiB cap).
- **Auth / bind guidance** — optional HTTP Basic Auth via `[web.auth]`
  (constant-time credential compare), a startup warning when binding a
  non-loopback address without auth, and README "Exposing the web view"
  guidance covering Basic Auth-behind-TLS and reverse-proxy setups.
- **Handler tests** — manager-backed tests for add/refresh/OPML-import
  (real fetch against an httptest backend with permissive validation), plus
  star toggle, cross-origin rejection, OPML round-trip, and Basic Auth.

---

### **Newspaper Front Page & Topic Clustering** — COMPLETED

The web view was reworked from a feed-list home into a newspaper, driven by
the article content rather than the feed roster.

- **Front page (`/`)** — masthead (dateline, nameplate, autofocused search),
  a single **lead story**, then **topical section blocks** flowing as true
  CSS columns with a rule. `/topic/{slug}` lists a section in full.
- **Feed management moved to `/feeds`** — the old index (feed list, add,
  refresh, delete, OPML) now lives there; `/` is a reading surface. Inner
  pages share a compact masthead partial; breadcrumbs point back to `/feeds`.
- **Topic engine (`internal/topics`)** — emergent sections via TF-IDF over
  title+description and greedy shared-term clustering. Dependency-free and
  deterministic (stable slugs), reads `storage` directly so it works under
  either search backend. Each article lands in exactly one section; the
  catch-all `Latest` is always last.
- **Data-quality guards (important)** — real
  feeds emit undated (zero-time) and future-dated articles; BoltDB's date
  index floats zero-time to the top, so naive newest-first surfaced a feed
  about-page as the lead. `rankFunc` ranks zero/future articles stale so
  they never lead or dominate. URLs and aggregator/statuspage boilerplate
  (HN "points", statuspage "scheduled/completed", months, timezones) are
  stripped/stopworded to stop bogus topics ("tildes", "url", "utc").

**Known limitations (candidates for follow-up):**

- No stemming, so near-synonyms split into separate sections
  (`Russia` vs `Russian`, singular vs plural). Light stemming or a synonym
  merge step would consolidate them.
- The catch-all `Latest` is large (~70% of a 132-feed corpus). Single-term
  clustering leaves a long tail; multi-term topic labels, co-occurrence
  seeding, or per-feed-category hints could section more of it.
- Topics recompute per request over the top `frontCorpus` (400) articles.
  Fine at this scale; revisit with a cache keyed on corpus signature if the
  corpus grows large.

---

### **Feed-management page (`/feeds`) redesign** — COMPLETED

Reworked from the pre-overhaul flat list to match the newspaper polish.
Observed issues (132-feed real DB) and their fixes:

- [x] Flat alphabetical wall → client-side **filter box** + **sort**
      (A–Z / Unread / Updated) in the toolbar. Progressive enhancement: the
      list is fully rendered and label-sorted server-side, so it stays usable
      with JS off; the controls are hidden until `<body>.has-js`.
- [x] Indistinguishable duplicates → every row carries a **host+path
      subtitle** (`feedSource`), shown whenever it differs from the display
      label. Three same-titled `arxiv.org` feeds now split by path
      (`/rss/cs.AI` vs `/rss/cs.LG`). Sort breaks label ties on URL so dups
      land adjacent.
- [x] Bare unread badges → labeled **"N unread"** plus a meta row with
      **article count** and **last-fetched** ("updated …" / "never fetched").
- [x] Unstyled file input → real **toolbar** (add-feed row, filter/sort row,
      OPML row); the native file input is driven by a link-styled `<label>`.
- [x] Broadsheet type scale → reuses the shared `masthead`/`crumbs` chrome
      and a hairline rule under the toolbar, consistent with `/topic/{slug}`.

**Gap closed:** `storage.Feed` now persists `LastError`/`LastErrorAt`, written
by `Manager.RefreshFeed`, and `/feeds` renders a "fetch failed" badge plus a
meta-row line — see "Persist feed fetch-error state" below.

Code: `internal/web/handlers.go` (`handleFeeds`, `feedCounts`),
`internal/web/front.go` (`feedLabel`, `feedHost`, `feedSource`),
`internal/web/templates/feeds.html`, `style.css`, `app.js`.

---

### **Code-review hardening** — COMPLETED

A review of the web-viewer branch produced a set of correctness, security,
and consistency fixes, each landed as its own commit:

- **storage** — delete the stale date-index key when a re-saved article's
  `Published` changes, so it can't appear twice in newest-first pagination
  or float a zero-time key to the top (regression-tested).
- **web** — buffer template rendering so a mid-render error returns a clean
  500; bound the OPML upload with `MaxBytesReader` and remove spilled temp
  files; strip CSS comment/escape chars from the custom font value; add a
  `:focus-visible` ring (WCAG 2.4.7); replace the JS-only delete `confirm()`
  with a no-JS `<details>` disclosure; emit `data-updated=0` for
  never-fetched feeds.
- **search** — close a bleve index opened after a lock timeout instead of
  leaking it; close the test engine before temp cleanup (fixed a flaky
  `RemoveAll` race); select permissive path validation via an explicit
  argument rather than sniffing the temp-dir prefix.
- **opml** — cap `Parse` input with an `io.LimitReader` (Go's xml does not
  expand custom entities, so this only bounds size).
- **cli / config / tui** — `--force` canonical with `--force-refresh` as a
  deprecated alias; `toggle_star` added to the keybinding collision check
  and example config; keyhandler consistency and a doc-comment fix.

---

### **Auto light/dark theming across all front-ends** — COMPLETED

All three surfaces now follow the system light/dark setting:

- **Web** — already adapted via CSS `@media (prefers-color-scheme: dark)`
  over the `:root` design tokens; no change needed beyond confirming the new
  feed-error badge uses the established danger red.
- **TUI chrome** — previously only the glamour *article body* adapted; the
  lipgloss UI chrome (`branding.go`) was hardcoded for a dark background, so
  text and muted/unread/header hues were near-invisible on a light terminal.
  Introduced `applyPalette(dark)`, which flips the background-dependent
  colors (new adaptive `FgColor` for body/modal text, plus `MutedColor`,
  `UnreadColor`, `SecondaryColor`) and rebuilds every dependent style; brand
  and status hues stay fixed. The dark/light bit is derived from the existing
  `resolveGlamourStyle` resolution (`glamourStyleIsDark`) — reusing the
  macOS plist watcher, `COLORFGBG`, and explicit-override logic rather than a
  lipgloss OSC 11 probe (which the project avoids for its multi-second
  startup hang). Applied at construction and on every live theme change, so
  toggling system appearance re-themes the whole UI, not just the reader.

Code: `internal/tui/branding.go`, `internal/tui/theme.go`,
`internal/tui/app.go`. Tested in `theme_test.go`
(`TestGlamourStyleIsDark`, `TestApplyPalette_FlipsBackgroundDependentColors`).

---

### **Background service, mDNS (fwrd.local), and favicon** — COMPLETED

Make the web view a first-class always-on surface reachable by name:

- **mDNS** (`internal/mdns`, hashicorp/mdns) — `serve --mdns` advertises an
  `fwrd.local` A record plus an `_http._tcp` service on the bind port, so any
  LAN device reaches `http://fwrd.local:8080` without DNS/hosts/static IP.
  IPv4-only, link-local; warns on a loopback bind (the name would resolve to
  an unreachable interface). Coexists with Avahi on Linux. `--mdns-name`
  changes the label.
- **Service** (`internal/service`) — `fwrd service install/uninstall` writes
  and activates a per-user systemd unit (Linux) or launchd LaunchAgent
  (macOS), pointing at the running binary and forwarding `--config`/`--db`.
  Defaults to `--addr 0.0.0.0:8080 --mdns`. Build-tag split per OS with an
  unsupported-platform stub; unit/plist rendered with `text/template` (plist
  values XML-escaped via a template func). Pure render functions are tested;
  install/uninstall shell out to systemctl/launchctl.
- **Favicon** — a theme-aware SVG (forward triangle, `prefers-color-scheme`)
  embedded and served at `/favicon.svg` + `/favicon.ico`, linked from
  `layout.html`.

---

### **Styled CLI logging (charmbracelet/log)** — COMPLETED

The CLI's operational output (startup, plugin load/registration, `serve`
diagnostics) moved from stdlib `log.Printf` and `fmt.Fprintln` banners to
`charmbracelet/log`: colored level badges, timestamps, and `key=value`
rendering, TTY-aware (plain when piped). A `pluginLogger` adapts it to the
`plugins/lua` printf Logger so plugin events share the styled output; serve
and plugin-load diagnostics now carry structured fields (`dir`/`err`/`fix`/
`url`). The file-based `internal/debuglog` sink is unchanged and separate.

Code: `cmd/rss/main.go`.

---

### **Search field newspaper restyle** — COMPLETED

The web search input was the lone SaaS-styled element (rounded box, focus
glow) clashing with the newspaper chrome. Reworked into a fill-in-the-blank
ruled underline: transparent background, ink-rule underline, reading serif
with an italic placeholder, centered on the hero. Focus is a thickened accent
underline rather than a box — the hero input is autofocused, so an outline box
would frame it on every load. a11y preserved: `--ink-rule` boundary clears
WCAG 1.4.11 (3:1); the focus underline meets 2.4.7 / 2.4.11; placeholder keeps
full contrast (`opacity:1`). A `pageshow`/`event.persisted` handler refocuses
the autofocused search on back/forward-cache restore (autofocus does not
re-fire on a bfcache restore).

Code: `internal/web/templates/style.css`, `internal/web/templates/app.js`.

---

### **Persist feed fetch-error state** — COMPLETED

`storage.Feed` gained `LastError` (message; `""` = last attempt succeeded) and
`LastErrorAt` (timestamp). `Manager.refreshFeedByID` records both on
fetch/parse failure — saving the feed on the error paths that previously
returned without a write — and clears them on success or 304. `LastFetched`
still tracks the last *successful* fetch, so a feed can show "updated 3d ago"
and a "fetch failed" badge at once. `/feeds` renders the badge plus a red
meta-row line (`last refresh failed <when>`), both with the full error in a
`title=` tooltip. Regression-tested in `manager_test.go`
(`TestRefreshFeed_RecordsAndClearsError`).

TUI badge is still open as an optional follow-up.

Code: `internal/storage/models.go`, `internal/feed/manager.go`,
`internal/web/templates/feeds.html`, `internal/web/templates/style.css`.

---

## Optional Future Enhancements

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