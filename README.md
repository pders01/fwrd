# fwrd

A fast, terminal-based RSS feed aggregator with full-text search capabilities, built with Go and Charm.sh tools. **fwrd** helps you navigate through time as expressed by new content, while being a respectful netizen that honors server resources.

## Features

- **Triple Interface**: Interactive TUI (Bubble Tea) + Command-line interface (Cobra) + web view (`fwrd serve`)
- **Newspaper web view**: The web front page is a newspaper — a lead story plus emergent topic sections clustered from recent articles; feeds are managed at `/feeds`
- **Auto light/dark**: Every front-end follows the system light/dark setting — the web view via CSS, the TUI by detecting the terminal/OS appearance (override with `[ui] theme`)
- **Zero-config LAN access**: `serve --mdns` advertises the web view at `https://fwrd.local:8080` over mDNS; `fwrd service install` runs it as a systemd/launchd background service; `fwrd net up` exposes it at a bare `https://fwrd.local` (ports 80+443) via a dedicated alias IP + firewall redirect, without colliding with the host's own privileged ports
- **Full‑text search**: Bleve‑powered search across feeds and articles with debounced input
- **Comprehensive CLI**: Complete feed management from command line (add, list, delete, refresh)
- **Smart caching**: Honors ETag and Last-Modified; handles 304/Retry-After responses
- **Security-focused**: URL validation, path sanitization, content size limits
- **Media integration**: Detects media types and opens in appropriate applications
- **Local storage**: BoltDB-backed offline reading with optimized indexing
- **Lua scriptable plugins**: Drop a `.lua` file into `~/.config/fwrd/plugins/` to add a feed-URL handler — no recompile, hot-reload included
- **Logging**: Styled, leveled CLI output (charmbracelet/log) for startup and plugin/serve diagnostics, plus a separate file-based debug log with configurable levels
- **Cross-platform**: Builds for Linux, macOS, Windows (amd64, arm64, arm)

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

### Interactive TUI Mode

```bash
# Run interactive TUI (default mode)
./fwrd

# Skip startup banner
./fwrd --quiet

# Enable debug logging
./fwrd --debug

# Custom config and database
./fwrd --config /path/to/config.toml --db /path/to/feeds.db
```

The interface follows your system light/dark setting automatically. Force a
mode (or cycle it live with `ctrl+t`) via config:

```toml
[ui]
theme = "auto"   # "auto" (default, detect) / "light" / "dark"
```

### Command Line Interface

```bash
# Show version
./fwrd version

# Generate default config
./fwrd config generate

# Feed management
./fwrd feed add "https://example.com/feed.xml"
./fwrd feed list
./fwrd feed refresh
./fwrd feed delete <feed-id>

# Plugin inspection
./fwrd plugins list

# Get help for any command
./fwrd --help
./fwrd feed --help
```

### Web Mode

Serve a web view of your feeds. The front page is a newspaper: a lead story
and emergent topic sections clustered from your most recent articles, with
search up top. Feed management lives at `/feeds`. Unlike the TUI — which
converts HTML to terminal markdown — the web view renders article content
as sanitized HTML, the form it was authored in.

```bash
./fwrd serve                          # https://127.0.0.1:8080 (self-signed)
./fwrd serve --addr 127.0.0.1:9000    # custom bind address
./fwrd serve --tls=false              # plain HTTP
./fwrd serve --addr 0.0.0.0:8080 --mdns  # LAN-reachable at https://fwrd.local:8080
```

Near-parity with the TUI/CLI:

- Newspaper front page: a lead story plus topic sections built from recent
  articles; a section opens in full at `/topic/{slug}`
- Manage feeds at `/feeds` — add, refresh (per-feed or all), and delete, with
  unread counts, last-fetched time, and a **fetch-failed badge** when a feed's
  last refresh errored
- Read full article HTML, media links, and the original source link
- Full-text search (Bleve), front-and-center and autofocused
- Mark articles read/unread and star/unstar
- Import and export subscriptions as OPML
- Follows your system light/dark setting (CSS `prefers-color-scheme`)

State-changing actions are no-JS `POST` forms guarded by a same-origin
check, so the page works without JavaScript while rejecting cross-site
(CSRF) form submissions. A small progressive-enhancement script upgrades
the read/star toggles to update in place (no full reload) when JavaScript
is available; with it off, the forms fall back to the same POST endpoints.

The server holds the database open for its lifetime, so it cannot run
against the same `--db` (or search index) as a concurrent TUI or second
`serve` — BoltDB and the Bleve index are single-process.

#### Exposing the web view

The default `127.0.0.1` bind is reachable only from the local machine,
which suits personal use and needs no auth. To reach the web view from
another device you must bind a non-loopback address (e.g.
`--addr 0.0.0.0:8080`) — and then **anyone who can reach that address can
read and modify your feeds.** `serve` prints a warning when it binds
off-box without auth configured.

Two ways to protect it:

- **Built-in HTTP Basic Auth.** Set credentials in your config; every
  request (read and write) then requires them:

  ```toml
  [web]
  font = "serif"

  [web.auth]
  username = "you"
  password = "a-long-random-secret"
  ```

  Basic Auth sends credentials base64-encoded, not encrypted — but `serve`
  speaks HTTPS by default (below), so on the built-in server they travel
  encrypted. Behind a plain-HTTP reverse proxy, terminate TLS there.

- **Reverse proxy.** Front fwrd with nginx/Caddy/Traefik handling TLS and
  authentication, and keep fwrd bound to `127.0.0.1`. This is the
  recommended setup for anything beyond a trusted LAN.

#### HTTPS / TLS

`serve` runs over **HTTPS by default** with an auto-generated certificate;
there is nothing to configure for encryption. A cleartext request to the serve
port is answered with a `308` redirect to `https://` on the **same host:port**,
so existing `http://host:PORT` bookmarks keep working. Use `--tls=false` for
plain HTTP.

The certificate comes from one of three sources (`--tls-mode`, or `[web.tls]`):

- **`self-signed`** (default) — a self-signed leaf, regenerated automatically
  when the advertised host set changes. Zero setup, but browsers show a
  one-time "not private" warning. Persisted under `~/.fwrd/tls`.
- **`local-ca`** — a local CA plus a leaf it signs, so you can be warning-free.
  See [Trusting the local CA](#trusting-the-local-ca) below.
- **`file`** — bring your own: `--tls-cert cert.pem --tls-key key.pem` (setting
  these selects the file source regardless of mode).

```toml
[web.tls]
enabled = true          # false → plain HTTP
mode    = "self-signed" # or "local-ca" / "file"
# cert_file = "/path/cert.pem"   # file mode
# key_file  = "/path/key.pem"
# dir       = "~/.fwrd/tls"      # where generated certs live
```

The generated certificate's SANs cover `localhost`, `127.0.0.1`/`::1`, the host
name, `<mdns-name>.local`, every `--mdns-ip`, and a concrete `--addr` host.

##### Trusting the local CA

In `local-ca` mode fwrd writes a local certificate authority to
`~/.fwrd/tls/ca.pem` and signs the server certificate with it. Browsers stay
warning-free **once that CA is trusted on each device that visits** — a
one-time step per device (the CA is stable; fwrd reuses it across cert
regenerations).

```bash
# macOS — trust system-wide (Safari and Chrome):
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ~/.fwrd/tls/ca.pem

# verify it took, then fully quit and reopen the browser:
security verify-cert -c <(openssl s_client -connect fwrd.local:8080 </dev/null 2>/dev/null | openssl x509)

# remove trust later:
sudo security delete-certificate -c "fwrd local CA" /Library/Keychains/System.keychain
```

On **iOS/Android** install `ca.pem` as a profile / user credential. On **Linux**
copy it into the system anchors (e.g. `/usr/local/share/ca-certificates/` +
`sudo update-ca-certificates`); Chrome/Firefox also keep their own NSS store.

> **Caveat — the CA can sign for any host.** A device that trusts this CA will
> trust **anything** the CA's key signs, not just fwrd. The key
> (`~/.fwrd/tls/ca-key.pem`, mode `0600`) therefore must stay private: whoever
> reads it can mint trusted certificates for any domain and MITM those devices.
> Keep it on the serving host, never copy it (copy only `ca.pem`), and prefer
> `self-signed` if you are not comfortable with that trade-off.

#### Reach it at `fwrd.local` (mDNS)

`--mdns` advertises the web view on the local network over multicast DNS, so
any device on the same LAN can reach it at `https://fwrd.local:8080` — no DNS,
hosts file, or static IP. Change the label with `--mdns-name <name>`
(advertised as `<name>.local`). (mDNS still advertises an `_http._tcp` record;
only the URL scheme differs.)

mDNS is link-local and the advertised address is a LAN interface, so `--mdns`
only makes sense with a non-loopback bind (`--addr 0.0.0.0:8080`); a
loopback-bound server logs a warning because the name would resolve to an
unreachable interface. On Linux it coexists with a running Avahi.

On a multi-homed host (several LANs at once), `--mdns` runs one responder per
interface, each answering with only the address reachable on the subnet the
query arrived from — so `fwrd.local` resolves correctly on every LAN, and a
client never gets an address on a subnet it can't route to. Virtual interfaces
(VM/container bridges, VPN tunnels, AirDrop) are skipped automatically;
restrict to specific interfaces with `--mdns-iface en0,en9`, or pin to a single
address with `--mdns-ip <ip>` (which advertises that one subnet only).

#### Run it as a background service

Install `fwrd serve` as a per-user service — a systemd user unit on Linux, a
launchd LaunchAgent on macOS. No root; it writes under your home directory.

```bash
./fwrd service install     # defaults to --addr 0.0.0.0:8080 --mdns, HTTPS on
./fwrd service uninstall
```

`install` writes the unit/plist (pointing at the running binary, forwarding
any `--config`/`--db` you pass), then enables and starts it. Override the bind,
mDNS name, or TLS with the same `--addr` / `--mdns` / `--mdns-name` /
`--tls` / `--tls-mode` flags — they are forwarded verbatim into the unit.
Because the default bind is LAN-facing and unauthenticated, set `[web.auth]`
(see above) when installing on a shared network; HTTPS (the default) keeps
those credentials encrypted in transit.

If the chosen port is already in use, `serve` now fails fast with a clear
error instead of pretending to start. As a background service it retries a few
times and then gives up: on Linux the unit enters `failed`
(`systemctl --user status fwrd`); on macOS launchd keeps the error in
`~/.fwrd/serve.err.log` (see [Viewing logs](#viewing-logs)).

#### Serving on port 80 (`fwrd net`)

To reach the web view at a bare `https://fwrd.local` (no `:8080`), fwrd needs to
answer on ports 443 and 80 — privileged ports that a host process (nginx,
Docker, …) may already hold. `fwrd net` sidesteps both problems without binding
them in the server itself:

```bash
sudo fwrd net up --alias-ip 192.168.1.240
# then, as your normal user (serve is HTTPS by default):
fwrd serve --addr 0.0.0.0:8080 --mdns --mdns-ip 192.168.1.240
# reachable from any LAN device at:  https://fwrd.local
#   (http://fwrd.local auto-upgrades to it)
sudo fwrd net down            # remove the alias IP + redirect
fwrd net status               # show the active binding, if any
```

`net up` maps **both `:80` and `:443`** of the alias onto the serve port, so the
bare `https://fwrd.local` works and a cleartext `http://fwrd.local` 308-upgrades
to it. The browser warning is one-time per device; for a warning-free name,
trust the local CA once (`fwrd serve --tls-mode local-ca`, see the HTTPS section
above).

On a multi-homed host (several LANs at once), repeat `--alias-ip` once per LAN
to get `https://fwrd.local` on each, and advertise them all:

```bash
sudo fwrd net up --alias-ip 192.168.1.240 --alias-ip 192.168.178.240
fwrd serve --addr 0.0.0.0:8080 --mdns --mdns-ip 192.168.1.240 --mdns-ip 192.168.178.240
```

> **Cleartext-only mode.** To skip HTTPS entirely, pass `--https=false` to
> `net up` (maps `:80` alone) and `--tls=false` to `serve` (plain HTTP) — then
> the bare name is `http://fwrd.local` with no TLS and no warning.

`--iface` is auto-detected from each alias IP's subnet (the IP already says
which network it's on), so you just pick an unused IP on each LAN; pass
`--iface` to override.

This networking (mDNS + the port-80 alias/redirect) lives in a standalone,
reusable library — [`github.com/pders01/dotlocal`](https://github.com/pders01/dotlocal) —
for any app built on the same `go:embed`'d local-service pattern.

`net up` gives fwrd its **own** LAN IP (an alias on your interface) and installs
a firewall redirect from that IP's port 80 to fwrd's unprivileged port — `pf`
on macOS, `nftables` on Linux. The redirect runs in the kernel's PREROUTING/rdr
path, *before* the socket lookup, so it works even when the host already binds
`0.0.0.0:80`; and because fwrd has a dedicated IP, the redirect never touches
the host's own port-80 traffic. mDNS then advertises `fwrd.local` for the alias
IP only (`serve --mdns-ip`).

Pick an `--alias-ip` that is on your LAN subnet and currently unused (outside
the DHCP pool). `net up`/`down` need `sudo` (interface + firewall changes). On
macOS the redirect is loaded into the `com.apple/fwrd` pf sub-anchor — which
the stock `rdr-anchor "com.apple/*"` already evaluates — so `/etc/pf.conf` is
**never modified**; teardown just flushes that sub-anchor. The binding is
**not** reboot-persistent — re-run `fwrd net up` after a reboot. Linux and
macOS only.

#### Viewing logs

```bash
./fwrd logs                 # tail fwrd's debug log (~/.fwrd/fwrd.log)
./fwrd logs -f              # follow live
./fwrd logs -n 500          # last 500 lines
./fwrd logs --service       # the background service's output instead
```

`logs` is a thin wrapper: the default reads the debug log file, while
`--service` streams the service's output — `journalctl --user -u fwrd` on
Linux, the LaunchAgent's `~/.fwrd/serve.*.log` files on macOS.

#### Audit log

`serve --audit` records **every HTTP request that passes through fwrd** as a
JSON-lines log at `~/.fwrd/audit.log` — both directions:

- **inbound** — requests served to the web UI (`dir:"in"`): method, request
  URI, status, response bytes, duration, client IP, Host, whether it was TLS,
  and the Basic-Auth username (never the password);
- **outbound** — requests fwrd makes (`dir:"out"`): feed fetches
  (`source:"feed"`) and Lua-plugin `http.get` calls (`source:"plugin"`), with
  the URL, status, and any transport error.

```bash
fwrd serve --audit                 # enable for this run
tail -f ~/.fwrd/audit.log | jq .   # one JSON object per line
```

It is **off by default** — it records every browse and every feed fetch, which
has privacy and disk-growth cost. Enable it persistently, or relocate the file,
via config:

```toml
[web.audit]
enabled = true
path    = "~/.fwrd/audit.log"   # default
```

The flag overrides the config (`--audit` forces on). The log is append-only and
not rotated; manage growth with `logrotate` or by truncating it. fwrd records
the transport-level peer address as the client IP and does **not** trust
`X-Forwarded-For`; behind a reverse proxy the IP is the proxy's.

#### OPML on the command line

```bash
./fwrd feed export feeds.opml   # write all subscriptions (use "-" for stdout)
./fwrd feed import feeds.opml   # add each listed feed (use "-" for stdin)
```

Import skips feeds already subscribed and reports any that fail to fetch
without aborting the rest.

### Keyboard Shortcuts (default)

Note: The modifier key defaults to `ctrl` and can be changed in config.

- Feeds: `ctrl+n` add • `ctrl+r` refresh • `ctrl+x` delete • `Enter` view articles
- Articles: `ctrl+u` toggle read • `ctrl+f` star/unstar • `Enter` read • `esc` back
- Reader: `ctrl+o` open media/links • `ctrl+f` star/unstar • `esc` back
- Global: `ctrl+s` search • `ctrl+t` cycle theme (auto/light/dark) • `q` quit

### Search

- `ctrl+s` opens search. If opened from the reader view, it searches inside the current article; otherwise it searches globally across all feeds and articles. When no in‑article matches are found, fwrd automatically falls back to a global search.
- Input is debounced (~200ms) to keep the UI responsive. A short status flash shows the result count.
- Search is backed by a Bleve index by default:
  - Default DB path `~/.fwrd/fwrd.db` ⇒ index at `~/.fwrd/index.bleve`
  - Custom DB path ⇒ index sits next to the DB with a `.bleve` suffix
- The index is created on first run, re‑indexed at startup, and updated on add/refresh/delete of feeds and articles.
- To force a rebuild, remove the index directory and start fwrd again.

Status Bar

- The bottom status bar provides brief messages and a subtle spinner during long‑running actions like feed refresh or article loading.

## Plugins

fwrd ships with a Lua scriptable plugin runtime. Plugins enhance feed
URLs at add-time — for example, turning `https://reddit.com/r/golang`
into the canonical RSS endpoint, or resolving a YouTube `@handle` to
the channel's feed.

### Where plugins live

```
~/.config/fwrd/plugins/
  reddit.lua      # shipped default
  youtube.lua     # shipped default
  yourplugin.lua  # add your own
```

The directory is seeded with the bundled defaults on first run. fwrd
hot-reloads the directory: editing a `.lua` file picks up changes
without a restart, and deleting a file unregisters the plugin.

### Plugin shape

Each script returns a table with four required fields:

```lua
return {
  name = "example",
  priority = 50,                          -- higher wins when multiple plugins match
  can_handle = function(url)
    return string.find(url, "://example.com/", 1, true) ~= nil
  end,
  enhance = function(url)
    return {
      feed_url    = url .. "/rss",
      title       = "Example",
      description = "...",
      metadata    = { kind = "blog" },    -- optional, string-keyed string values
    }
  end,
}
```

### Sandbox surface

Scripts run on a sandboxed gopher-lua runtime:

- Allowed stdlib: `string.*`, `math.*`, `table.*`
- Removed: `io`, `os`, `package`, `debug`, `load*`, `dofile`,
  `loadfile`, `require`, `setmetatable`, `getmetatable`, `print`
- Host bindings:
  - `http.get(url[, {headers={...}}])` returns `(result, err)` where
    `result` is `{status, body, headers}`
  - `json.parse(s)` / `json.encode(v)` return `(value, err)`
  - `regex.match(pattern, subject)` returns the first capture group
    (or whole match) using Go RE2 syntax
  - `log.info(msg)` / `log.warn(msg)` route to fwrd's debug log

A 256 KiB script-size cap and a 30-second per-call timeout (matching
`AddFeed`) prevent runaway scripts. Plugin HTTP requests share the
fetcher's User-Agent and timeout so plugin-driven traffic looks like
fwrd to upstreams.

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

Bleve index debug (optional)

```bash
# Inspect the existing on-disk index (used for debugging)
go test -tags=bleve -v -run TestDebugExistingIndex ./internal/search
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
- `github.com/charmbracelet/log` - Styled CLI logging
- `github.com/spf13/cobra` - CLI framework
- `github.com/spf13/viper` - Configuration management
- `github.com/mmcdole/gofeed` - RSS/Atom parsing
- `go.etcd.io/bbolt` - Embedded database
- `github.com/blevesearch/bleve/v2` - Full‑text search engine
- `github.com/pelletier/go-toml/v2` - TOML configuration

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed information about the codebase structure.

Notes on Search
- The UI uses debounced input and shows brief status messages (500ms) for action feedback.
- The search engine prioritizes title/description/content (with sensible boosts) and also considers URL text.

## Media players

fwrd detects media types (video, image, audio, PDF) and tries players in order configured by platform. You can customize these in the config file.

Defaults (examples):
- macOS: Video iina→mpv→vlc • Image Preview/open • Audio mpv→vlc • PDF Preview/open
- Linux: Video mpv→vlc→mplayer • Image sxiv→feh→eog/xdg-open • Audio mpv→vlc→mplayer • PDF zathura→evince→xdg-open

If no specific player is found, fwrd falls back to the platform default opener (`open`, `xdg-open`, `start`).
