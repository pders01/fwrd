# TODO - Future Improvements

This document tracks remaining improvement opportunities and optional enhancements for the fwrd RSS aggregator.

## Current Status

**40 test files across 64 Go source files.** Per-package statement coverage
below (excludes `test/integration`, which needs port 8080 free — OrbStack
often holds it on this machine):

**Core modules well-covered (76–94%):**
- **OPML**: 94.1%
- **Configuration**: 90.6% (config loading, defaults, TLS defaults)
- **Validation**: 89.7% (path, URL, and security validation)
- **Feed Management**: 88.5% (RSS/Atom parsing, fetching, manager)
- **Media**: 80.5% (type detection and launcher)
- **Topics**: 80.1% (TF-IDF clustering)
- **Debug Logging**: 77.9%
- **Storage**: 77.6% (database operations and indexing)
- **Web TLS (`webtls`)**: 76.1% (cert sources, generation, mode switch)

**Mid-range:**
- **Lua plugins**: 75.2% · **Search**: 71.4% · **Web**: 70.3% ·
  **Plugins**: 55.6%

**Main testing gaps:**
- **Service**: 34.2% (install/uninstall shell out; only pure render tested)
- **TUI**: 32.7% (limited UI component testing — main opportunity)
- **CMD**: 18.7% (CLI commands — mainly integration-tested)

---

## Recent Additions

### **Web performance: O(1) feed counts + front-page cache** — COMPLETED

Profiling a real **169 MB / 8.7k-article** database (benchmarks point at a DB
copy via `FWRD_BENCH_DB`; they skip otherwise) found two page loads re-scanning
the whole corpus on every request:

- **`/feeds` counts: ~480 ms → ~0.3 ms.** `feedCounts` decoded *and sorted*
  every article of every feed (197 MB allocated) just to tally unread/total.
  A new **`articles_unread_by_feed`** index (one sub-bucket of unread IDs per
  feed) lets `Store.FeedStats()` read both counts via `Bucket.Stats().KeyN` in
  one transaction, decoding **no** article JSON. Total comes from the existing
  `articles_by_feed` index the same way. The index is maintained in
  `SaveArticles` (membership = `!Read`, idempotent), `mutateArticle` (on a Read
  flip), and `DeleteFeed`, and **back-filled once on Open** for existing DBs
  (metaBucket flag gates the rescan).
- **`/` and `/topic/{slug}`: ~40 ms → ~0 ms on a cache hit.** The TF-IDF topic
  model + feed-name map were rebuilt per request. `Server.frontView` now
  memoizes both, keyed on a new **`Store.WriteGen()`** counter (bumped on every
  mutation), so the model rebuilds only after a write — read/star state stays
  fresh, repeat loads are free. A transient load error serves the stale cache
  rather than a blank page.

**Migration note:** the first `serve`/TUI start on the **new binary** opens the
existing DB and back-fills the unread index once (~0.5 s scan on this DB), then
sets the flag so later opens pay nothing.

**Pre-existing follow-up (not a regression):** feed **refresh** re-saves parsed
articles, resetting `Read` to false for items still present in the feed XML
(the unread index faithfully mirrors this — the old decode-count did too). A
fix would preserve stored `Read`/`Starred` state on re-save in `SaveArticles`.

Code: `internal/storage/store.go`, `internal/web/{front,handlers,server}.go`.
Tested in `internal/storage/feedstats_test.go` (counts, transitions, on-open
rebuild, write-gen, gated decode-parity) and `internal/web/front_cache_test.go`
(cache reuse + write invalidation); benchmarks in `count_bench_test.go` /
`perf_bench_test.go`.

### **Request audit log (`internal/audit`)** — COMPLETED

`serve --audit` records **every HTTP request through fwrd**, both directions,
as JSON lines at `~/.fwrd/audit.log` (append-only, one object per line).

- **`internal/audit`** — a concurrency-safe, nil-safe `Logger` (writes are
  mutex-serialized; a nil `*Logger` is a no-op so callers hold a possibly
  disabled one without checks) and a unified `Record` whose direction-specific
  fields carry `omitempty`.
- **Inbound** — an `auditLog` middleware wraps the chain **outermost** (so it
  records the TLS `308` redirects and `401`/`403` rejections, not just requests
  that reach a handler), plus a status/bytes-capturing `ResponseWriter`. Logs
  method, request URI, status, byte count, duration, client IP, Host, TLS, and
  the Basic-Auth username (**never** the password).
- **Outbound** — a single `http.RoundTripper` on the shared feed/plugin client
  (`Manager.UseAuditLogger`) covers **both** feed fetches and Lua plugin
  `http.get`; a request-context tag attributes each to `source:"feed"` or
  `source:"plugin"`.
- **Config** — `[web.audit] enabled`/`path` (default **off**, `~/.fwrd/audit.log`);
  `serve --audit` flag overrides config. Off by default because it records every
  browse and every fetch (privacy + disk growth).
- Client IP is the transport peer; `X-Forwarded-For` is **not** trusted (behind
  a proxy the IP is the proxy's). Log is unrotated — manage with `logrotate`.

Tested in `internal/audit/audit_test.go` (JSON/omitempty/nil-safety,
RoundTripper success/error/passthrough) and `internal/web/audit_test.go`
(middleware status + IP capture). Verified end-to-end against a live `serve`.

Code: `internal/audit/audit.go`, `internal/web/server.go`,
`internal/feed/{fetcher,manager}.go`, `internal/plugins/lua/http.go`,
`internal/config/config.go`, `cmd/rss/main.go`.

### **HTTPS for `fwrd serve` (self-signed by default, pluggable cert source)** — COMPLETED

The web view now serves over **HTTPS by default** with no setup. A new
`internal/web/webtls` package abstracts the certificate behind a single
`Source` interface with three origins, selected by `--tls-mode` / `[web.tls]`:

- **`self-signed`** (default) — an auto-generated leaf (ECDSA P-256, ≤398-day
  validity), persisted under `~/.fwrd/tls` and regenerated only on expiry or
  when the advertised host set changes. One-time browser warning.
- **`local-ca`** — a long-lived local CA plus a leaf it signs; warning-free
  once the printed `ca.pem` is trusted per device. Leaf is written as a
  leaf+CA chain.
- **`file`** — bring-your-own via `--tls-cert` / `--tls-key` (presence selects
  the file source regardless of mode).

**Single-port TLS mux** (`internal/web/tlsmux.go`) is the enabler for
HTTPS-by-default without breakage: it peeks the first byte of each connection
(TLS ClientHello `0x16` → wrapped in `tls.Server`; anything else stays plain),
and the handler 308-redirects cleartext (`r.TLS == nil`) to `https://` on the
**same host:port**. So existing `http://host:PORT` bookmarks keep working,
auto-upgraded. (The `fwrd net` bare-`:80` flow is the exception — the 308 goes
to `:443`, which the alias redirect does not map; see Known limitations.)
Relies on `net/http` special-casing `*tls.Conn` to populate `r.TLS`. A short
peek deadline guards against a client that connects without sending.

Flags `--tls` / `--tls-mode` / `--tls-cert` / `--tls-key` (flag > `[web.tls]` >
default), forwarded through `service install`. SANs cover `localhost`,
`127.0.0.1`/`::1`, the host name, `<mdns-name>.local`, every `--mdns-ip`, and a
concrete `--addr` host. `serve`/mDNS log URLs switch to the active scheme.
`--tls=false` opts out.

**Known limitations:**

- dotlocal v0.3.0 advertises mDNS as `_http._tcp` only (no service-type
  param), so the SRV label stays `http` even though the URL is `https` —
  cosmetic; name resolution and the URL are unaffected.
- ~~`fwrd net` (bare `:80`) needs `--tls=false`~~ **RESOLVED.** `net up` (on
  dotlocal v0.4.0) now maps the port *set* `{80, 443}` of the alias onto the
  serve port, so bare `https://fwrd.local` (`:443`) serves and a cleartext
  `http://fwrd.local` 308-upgrades to it — no `--tls=false`. `--https=false`
  (net) + `--tls=false` (serve) restores the cleartext-only `:80` mode.
- **Host Mac itself still can't reach the bare name** (applies to `:443` too):
  pf `rdr` only catches traffic on a physical interface and macOS skips
  loopback (`set skip on lo0`). LAN peers get the bare name; on the host use
  `https://fwrd.local:<serve-port>`. Won't-fix (see the port-80 entry below).

Code: `internal/web/webtls/webtls.go`, `internal/web/tlsmux.go`,
`internal/web/server.go`, `internal/config/config.go`, `cmd/rss/main.go`,
`internal/service/`. Tested in `webtls_test.go`, `tlsmux_test.go`,
`service_unix_test.go`, `config_test.go`.

### **Port 80 (`fwrd net`), responsive web UI, fail-fast serve, `fwrd logs`** — COMPLETED

A round of usability fixes from real-world use:

- **`fwrd net up/down/status`** (backed by the extracted `dotlocal/port80`
  library) — expose the web view at a bare `http://fwrd.local` (port 80)
  without binding a privileged port and without colliding with a host process
  already on :80. It gives fwrd a dedicated LAN alias IP **per LAN** (repeatable
  `--alias-ip`, interface auto-detected from each IP's subnet) and installs a
  PREROUTING/rdr redirect from that IP's :80 to fwrd's unprivileged port — `pf`
  on macOS (loads into the `com.apple/fwrd` sub-anchor that the stock
  `rdr-anchor "com.apple/*"` already evaluates, so `/etc/pf.conf` is never
  touched), `nftables` on Linux (own `fwrd` table). The redirect precedes the
  socket lookup, so it works even when the host binds `0.0.0.0:80`. mDNS
  advertises each alias scoped to its subnet (`mdns.AdvertiseScoped` +
  repeatable `serve --mdns-ip`, also forwarded by `service install`). Root-only;
  state in the root-owned `/var/run/dotlocal/<name>.json`; not reboot-persistent.
- **Responsive web layout** — added a `@media (max-width: 40rem)` block: the
  masthead search drops to a full-width line, the two-up add-feed/sort toolbars
  collapse to one column, and `.page-head` wraps so action buttons never get
  pushed off-screen.
- **Centered reader column** — the article reader now narrows its whole `<main>`
  (`main.reading`, `--readw`) so crumbs + headline + body share one centered
  column under the full-width masthead, instead of hugging the left edge.
- **Scroll preservation** — returning from an article to the front page keeps
  the prior scroll position; the bfcache `pageshow` handler no longer yanks
  focus to the top search field (skips refocus when scrolled; `preventScroll`).
- **Fail-fast serve** — `serve` binds the port *before* logging "serving" or
  advertising mDNS, so an in-use port fails immediately with a clear hint
  (`web.Server.Listen`/`Serve` split) rather than a misleading "serving" line.
  The systemd unit gained `StartLimitIntervalSec`/`StartLimitBurst` so a
  persistent bind failure surfaces as a `failed` unit instead of an infinite
  restart loop.
- **`fwrd logs`** — convenience wrapper: tails `~/.fwrd/fwrd.log` by default, or
  the background service's output with `--service` (journalctl on Linux, the
  LaunchAgent's `serve.*.log` on macOS); `-f`/`-n` for follow/line-count.

The mDNS + port-80 machinery was **extracted into a standalone, reusable
library**, [`github.com/pders01/dotlocal`](https://github.com/pders01/dotlocal)
(packages `mdns`, `port80`, top-level `Run`), for any app using the same
`go:embed`'d local-service pattern; fwrd's `internal/mdns` and `internal/netbind`
were deleted in favour of it. fwrd now depends on the published **v0.3.0**.

`<name>.local` resolves across **every LAN** on a multi-homed host: Linux via a
self-hosted responder, **macOS via the system mDNSResponder** (`dns_sd` C API —
a self-hosted responder does not interoperate with Bonjour). macOS builds of
fwrd therefore require **cgo** (`CGO_ENABLED=1`).

**Known limitation (won't-fix):** on the **host Mac itself**, bare
`http://fwrd.local` (port 80) does not work — pf `rdr` only catches traffic
arriving on a physical interface, and macOS skips loopback (`set skip on lo0`).
Use `http://fwrd.local:5336` (or whatever `--addr` port) on the host; every
other LAN device gets the bare `:80` URL. True host uniformity would require a
root daemon binding `:80` on the alias IP (a different service model) and is not
pursued.

Code: `cmd/rss/main.go`, `internal/web/server.go`,
`internal/web/templates/{style.css,article.html,app.js}`, `internal/service/`,
`internal/debuglog/log.go`; library at `github.com/pders01/dotlocal`.



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
- ~~Topics recompute per request over the top `frontCorpus` (400)
  articles.~~ **RESOLVED** — the front-page topic model and feed-name map are
  now memoized in `Server.frontView`, keyed on `Store.WriteGen()`. See the
  performance entry below.

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

The TUI feed list now mirrors the badge: a failing feed shows a red
`✗ fetch failed` marker on its title and a `last refresh failed <when>: <msg>`
description line (`feedItem.Title`/`Description` in `internal/tui/app.go`,
regression-tested in `app_test.go`).

Code: `internal/storage/models.go`, `internal/feed/manager.go`,
`internal/web/templates/feeds.html`, `internal/web/templates/style.css`,
`internal/tui/app.go`.

---

## Next up

### **Wire `fwrd net` to redirect `:443` (bare `https://fwrd.local`)** — COMPLETED

dotlocal **v0.4.0** (`Options.Ports`) is released; fwrd now uses it. `net up`
maps the port set `{80, 443}` of the alias onto the serve port: bare
`https://fwrd.local` serves and `http://fwrd.local` 308-upgrades to it — the
`--tls=false` requirement is gone. A `net up --https` bool flag (default true)
toggles the `:443` mapping; `--https=false` + serve `--tls=false` restores the
cleartext-only `:80` mode. Port-set assembly is the pure `netPorts(port, https)`
helper (`cmd/rss/main.go`), unit-tested in `main_test.go` (`TestNetPorts`:
dedup when base is 443, https on/off, custom base). `net up`/`net status` logs
list all mapped ports and switch the printed URL scheme to the active one.

Code: `go.mod` (dotlocal v0.3.0 → v0.4.0), `cmd/rss/main.go`, `README.md`.

**Still owner-run (manual, root, separate LAN device):** `sudo fwrd net up
--alias-ip <ip>` → confirm pf/nft has both `:80` and `:443` rdr rules → `fwrd
serve --addr 0.0.0.0:8080 --mdns --mdns-ip <ip>` → from another LAN device:
`https://fwrd.local` serves, `http://fwrd.local` 308s to it. (Host Mac itself
still can't reach the bare name — pf skips `lo0`; unchanged.)

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