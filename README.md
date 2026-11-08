# fwrd

A terminal-based RSS feed aggregator with full-text search, built with Go and Charm.sh tools. **fwrd** provides terminal, command-line, and web readers backed by local storage and conditional HTTP requests.

> **Upgrading from v1?** Read [Upgrading to v2](#upgrading-to-v2) before opening an existing database.

## Features

- **Triple Interface**: Interactive TUI (Bubble Tea) + Command-line interface (Cobra) + web view (`fwrd serve`)
- **Newspaper web view**: The web front page is a source-balanced newspaper — a scored lead, stable editorial desks, and cross-source developing topics; feeds are managed at `/feeds`
- **Auto light/dark**: Every front-end follows the system light/dark setting — the web view via CSS, the TUI by detecting the terminal/OS appearance (override with `[ui] theme`)
- **Opt-in LAN access**: After configuring authentication and a LAN bind, `serve --mdns` advertises `https://fwrd.local:8080`. Optional user services and alias-IP/firewall redirects support background operation and bare `https://fwrd.local`; see [Exposing the web view](#exposing-the-web-view)
- **Full‑text search**: Bleve‑powered search across feeds and articles with debounced input
- **Comprehensive CLI**: Complete feed management from command line (add, list, delete, refresh)
- **Conditional fetching**: Sends ETag/Last-Modified validators and handles 304 responses
- **Security-focused**: URL validation, path sanitization, content size limits
- **Media integration**: Detects media types and opens in appropriate applications
- **Local storage**: BoltDB-backed offline reading with optimized indexing
- **Lua scriptable plugins**: Add a `.lua` feed-URL handler without recompiling; the local TUI supports hot reload
- **Logging**: Styled, leveled CLI output (charmbracelet/log) for startup and plugin/serve diagnostics, plus a separate file-based debug log with configurable levels
- **Cross-platform releases**: Linux (amd64, arm64, arm), macOS (amd64, arm64), Windows (amd64)

## Upgrading to v2

1. **Stop all fwrd processes**, including background services, and back up your
   configured database file and configuration before upgrading.
2. Install the new binary, or build it with **Go 1.26.10 or newer**. Restart the
   server and clients together; do not mix v1 and v2 processes.
3. **Review network exposure.** Newly installed services bind to `127.0.0.1:8080`
   with mDNS off. Existing units/plists retain their previous arguments, but an
   unauthenticated LAN bind now fails to start. Configure both `[web.auth]`
   credentials or deliberately opt in with `--allow-unauthenticated`.
4. For trusted intranet feeds, explicitly set `[feed] allow_private_networks = true`.
   DNS results and redirects now enforce the same destination restrictions as
   submitted URLs. Outbound proxy environment variables are not honored.
5. Relative database, index, certificate, and audit paths in configuration now
   resolve against the **configuration file's directory**, not the shell's
   working directory. Edit existing configuration rather than running
   `config generate`, which now refuses to overwrite files.
6. The first database open **transactionally rebuilds derived indexes**. Allow
   extra startup time for large stores. Migration errors leave the transaction
   uncommitted; do not delete source data to bypass them. Downgrading after
   migration is unsupported—restore your pre-upgrade backup if you need v1.

See [SECURITY_REMEDIATION.md](SECURITY_REMEDIATION.md) for the complete fix
checklist, validation results, and remaining trust assumptions.

## Installation

### Using Go

Requires Go 1.26.10 or newer. From a checkout of this repository:

```bash
go build -o fwrd ./cmd/rss
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

# Generate default config (refuses to overwrite an existing file)
./fwrd config generate

# Feed management
./fwrd feed add "https://go.dev/blog/feed.atom"
./fwrd feed list
./fwrd feed refresh
./fwrd feed delete <feed-id>
./fwrd feed desk <feed-id> technology  # assign a stable newspaper desk

# Inspect source/desk/topic diversity and lead scoring
./fwrd edition diagnostics
./fwrd edition diagnostics --json

# Plugin inspection
./fwrd plugins list

# Get help for any command
./fwrd --help
./fwrd feed --help
```

### Web Mode

Serve a web view of your feeds. The front page is a newspaper: a scored lead
story and stable desks composed from your most recent articles, with emergent
cross-source topics shown as secondary labels and search up top. Feed
management lives at `/feeds`. Unlike the TUI — which converts HTML to terminal
markdown — the web view renders article content
as sanitized HTML, the form it was authored in.

```bash
./fwrd serve                         # https://127.0.0.1:8080 (self-signed)
./fwrd serve --addr 127.0.0.1:9000     # custom loopback address
./fwrd serve --tls=false             # plain HTTP on loopback

# LAN: configure both [web.auth] credentials first (see below)
./fwrd serve --addr 0.0.0.0:8080 --mdns
```

LAN examples throughout this guide assume that authentication is configured;
see [Exposing the web view](#exposing-the-web-view). mDNS advertises an address:
it does not change the bind address or configure authentication.

Near-parity with the TUI/CLI:

- Newspaper front page: a scored lead plus source-balanced stable desks built
  from recent articles; desks open at `/desk/{slug}` and compatibility topic
  pages remain available at `/topic/{slug}`
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

#### Editorial desks and composition

Every subscription has an explicit stable desk assignment. The available desk
IDs are `world`, `politics-society`, `business`, `culture`, `science-health`,
`climate`, `food-travel`, `sport`, `technology`, and `unfiled`. New and legacy
subscriptions remain Unfiled until assigned; fwrd does not infer user intent
from a publisher domain. Change a source's desk on its page under `/feeds`, or
use either its URL or ID from the CLI:

```bash
fwrd feed desk <URL-or-ID> science-health
```

Desk metadata survives refreshes and local IPC access. OPML export records it
as `category="fwrd/desk/<stable-id>"`, and import restores it; other OPML
readers can safely ignore that category.

The front page considers the newest 2,000 candidate articles and selects up to
400 under explicit diversity ceilings: 24 stories per source identity, 60 per
assigned desk, and 10 from one source within one desk. These are ceilings, not
quotas, so a fully filed edition can contain fewer than 400 stories rather than
relax its balancing rules. Unfiled is a compatibility fallback that may fill
otherwise unused room while an older database is being organized.

Developing topics are clustered from article titles and descriptions *inside*
each desk and are promoted only when at least two distinct sources support
them. Lead ranking combines recency, topic support, desk-source support, and a
small starred signal; future and undated stories cannot lead. Inspect every
active limit, source/desk share, promoted-topic source count, fallback share,
and lead-score component with:

```bash
fwrd edition diagnostics
fwrd edition diagnostics --json
```

For new subscriptions, fwrd preserves both the URL entered by the user and any
resolved plugin/fetch URL so source provenance remains inspectable. It records
the subscription time and marks older or undated imported history read; this
onboarding rule is not retroactively applied to existing feeds, and refreshes
preserve existing read/star state. Re-adding an existing subscription is
idempotent. Feed validators, fetch timestamps and article content commit in
one transaction, so a failed save cannot suppress the next fetch with a 304.

The server holds the database and search index open for its lifetime. A TUI or
feed CLI command targeting the same `--db` automatically connects to that
`serve` process over local IPC, so they can run concurrently without bypassing
its feed plugins or search-index updates. A second `serve` for the same database
is still rejected because BoltDB and Bleve each require a single owner.
Storage reads use a separate connection from long-running feed operations, so
refreshes do not block browsing. Idle connections stay open; read-only requests
can reconnect and retry once after a dropped connection. A mutation can also
reconnect once after a retryable write failure if **zero request bytes** were
accepted by the socket. Header and body bytes both count: partial writes and
lost responses never trigger mutation replay, since the outcome may be unknown.
The retry stays within the original call deadline.

#### Exposing the web view

The default `127.0.0.1` bind is reachable only from the local machine and
requires no authentication. To allow other devices, configure authentication
and bind a non-loopback address such as `--addr 0.0.0.0:8080`.

Unauthenticated non-loopback serving is refused unless explicitly enabled with
`--allow-unauthenticated` (also supported by `service install`) or
`[web] allow_unauthenticated = true`. **With that exception enabled, anyone
who can reach the listener can read and modify your feeds.**

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

#### Configuration and outbound requests

Builds require **Go 1.26.10 or newer**. Nested environment settings use
underscore-separated names, for example `FWRD_DATABASE_PATH`,
`FWRD_WEB_AUTH_USERNAME`, and `FWRD_WEB_AUTH_PASSWORD`. Both authentication
fields must be supplied together. File-relative database, index, certificate,
and audit-log paths resolve against the configuration file's directory.
Generated/saved configuration files are private (`0600` on Unix), and
`config generate` refuses to overwrite an existing file.

Feed and Lua-plugin requests reject loopback, private and link-local
addresses at connection time, including DNS results and redirects. For a
trusted intranet reader, explicitly set `[feed] allow_private_networks = true`
(or `FWRD_FEED_ALLOW_PRIVATE_NETWORKS=true`). This relaxes the boundary for all
subscriptions/plugins in that process; do not enable it for untrusted users.
Outbound proxy environment variables are intentionally ignored because proxy-side
DNS resolution would bypass this policy.

#### HTTPS / TLS

`serve` runs over **HTTPS by default** with an auto-generated certificate;
there is nothing to configure for encryption. A cleartext request to the serve
port is answered with a `308` redirect to `https://` on the **same host:port**,
so existing `http://host:PORT` bookmarks keep working. Use `--tls=false` for
plain HTTP.

The certificate comes from one of three sources (`--tls-mode`, or `[web.tls]`):

- **`self-signed`** (default) — a self-signed leaf, persisted under `~/.fwrd/tls`.
  It renews near expiry and is regenerated when the configured host set changes
  on restart. Browsers warn unless the certificate is trusted; renewal can
  require trusting the replacement certificate.
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
Generated certificates renew during TLS handshakes without requiring a restart.
A transient renewal error retains a still-valid certificate, never an expired
one. File-mode certificates are loaded at startup; restart after replacing them.

##### Trusting the local CA

In `local-ca` mode fwrd writes a local certificate authority to
`~/.fwrd/tls/ca.pem` and signs the server certificate with it. Browsers stay
warning-free **once that CA is trusted on each device that visits**. The CA
is reused across leaf renewals until it too needs rotation. When the CA rotates,
install the replacement `ca.pem` on each client again.

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

With a LAN bind and authentication configured, `--mdns` advertises the web
view over multicast DNS at `https://fwrd.local:8080`—no manual DNS or hosts-file
entry is needed. Change the label with `--mdns-name <name>`
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

> **⚠️ Don't hardcode `--mdns-ip` in a launchd plist or systemd unit.** Your
> LAN IP can change via DHCP, leaving the service advertising a stale address
> and `fwrd.local` unreachable. Use `--mdns-iface` (interface names are stable)
> to let fwrd auto-discover the current IP on each interface. The `service install`
> command forwards both flags — see below.

#### Run it as a background service

Install `fwrd serve` as a per-user service — a systemd user unit on Linux, a
launchd LaunchAgent on macOS. No root; it writes under your home directory.

```bash
./fwrd service install     # private default: 127.0.0.1:8080, HTTPS on
./fwrd service uninstall
```

`install` writes the unit/plist (pointing at the running binary, forwarding
any `--config`/`--db` you pass), then enables and starts it. Override the bind,
mDNS name, or TLS with the same `--addr` / `--mdns` / `--mdns-name` /
`--mdns-iface` / `--tls` / `--tls-mode` flags. Paths are resolved before
installation, then arguments are encoded for the platform's service manager.

```bash
# After configuring [web.auth], auto-detect IPs on en0 and en9
./fwrd service install --addr 0.0.0.0:5336 --mdns --mdns-iface en0,en9 --tls-mode local-ca
```

Using `--mdns-iface` instead of `--mdns-ip` keeps the service working across
DHCP address changes — fwrd resolves each interface's current IP at startup.
If you do need fixed addresses (e.g. alias IPs from `fwrd net up`), pass
`--mdns-ip`; be aware those IPs must stay in sync with your network setup.
The default bind is loopback-only and mDNS is off. Configure `[web.auth]`
before opting into a LAN bind; HTTPS (the default) keeps those credentials
encrypted in transit. Service configuration, database and certificate paths
are resolved before installation, so the service does not depend on your
shell's working directory. Environment-based service settings must also be
provided in the service manager's environment; terminal variables are not
persisted as credentials in unit/plist arguments.

If the chosen port is already in use, `serve` fails fast with a clear error.
On Linux, systemd retries within the unit's five-starts-per-minute limit and
then enters `failed` (`systemctl --user status fwrd`). On macOS, the launchd
agent uses `KeepAlive=true` and continues relaunching; errors appear in
`~/.fwrd/serve.err.log` (see [Viewing logs](#viewing-logs)).

#### Serving on port 80 (`fwrd net`)

To reach the web view at a bare `https://fwrd.local` (no `:8080`), fwrd needs to
answer on ports 443 and 80 — privileged ports that a host process (nginx,
Docker, …) may already hold. `fwrd net` sidesteps both problems without binding
them in the server itself:

```bash
sudo fwrd net up --alias-ip 192.168.1.240
# After configuring [web.auth], run as your normal user (HTTPS by default):
fwrd serve --addr 0.0.0.0:8080 --mdns --mdns-ip 192.168.1.240
# reachable from any LAN device at:  https://fwrd.local
#   (http://fwrd.local auto-upgrades to it)
sudo fwrd net down            # remove the alias IP + redirect
fwrd net status               # show the active binding, if any
```

`net up` maps **both `:80` and `:443`** of the alias onto the serve port, so the
bare `https://fwrd.local` works and a cleartext `http://fwrd.local` 308-upgrades
to it. For a warning-free name, use `--tls-mode local-ca` and trust its CA on
each client (see [Trusting the local CA](#trusting-the-local-ca)). CA rotation
requires updating that trust.

On a multi-homed host (several LANs at once), repeat `--alias-ip` once per LAN
to get `https://fwrd.local` on each, and advertise them all:

```bash
sudo fwrd net up --alias-ip 192.168.1.240 --alias-ip 192.168.178.240
# Requires both [web.auth] credentials:
fwrd serve --addr 0.0.0.0:8080 --mdns --mdns-ip 192.168.1.240 --mdns-ip 192.168.178.240
```

> **Cleartext-only mode.** To skip HTTPS entirely, pass `--https=false` to
> `net up` (maps `:80` alone) and `--tls=false` to `serve` (plain HTTP) — then
> the bare name is `http://fwrd.local`. This provides **no transport encryption**;
> do not send Basic Auth credentials over an untrusted network in this mode.

`--iface` is auto-detected from each alias IP's subnet (the IP already says
which network it's on), so you just pick an unused IP on each LAN; pass
`--iface` to override.

This networking (mDNS + the port-80 alias/redirect) lives in a standalone,
reusable library — [`github.com/pders01/dotlocal`](https://github.com/pders01/dotlocal) —
for any app built on the same `go:embed`'d local-service pattern.

`net up` gives fwrd its **own** LAN IP (an alias on your interface) and installs
firewall redirects from that IP's ports 80 and 443 (by default) to fwrd's
unprivileged port—`pf`
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
  the redacted URL, status, and a sanitized transport error.

Both directions remove URL userinfo, fragments, and query data. URL paths
remain visible and may contain sensitive identifiers. Files are opened with
private permissions on Unix, but old log contents and backups are not rewritten;
review previously recorded secrets if applicable.

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
- A new or empty index is populated from the database. A nonempty existing
  index is reused and updated incrementally on add/refresh/delete; it is not
  rebuilt on every startup.
- A TUI connected to `serve` uses store-backed basic search rather than opening
  the server's Bleve index. Its scoring and coverage differ from web/local Bleve
  search: it scans at most 200 recent articles per feed.
- To force a rebuild, stop every process owning the index, remove the configured
  index directory (not the database file), and restart its owner.

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

`$XDG_CONFIG_HOME/fwrd/plugins/` overrides the directory shown above when
`XDG_CONFIG_HOME` is set. Bundled defaults are seeded when missing.

The **local TUI** watches this directory: edits reload plugins and deleting a
file unregisters its plugin. `serve` loads plugins at startup and must be
restarted to pick up changes; an IPC-connected TUI uses those server-owned
plugins. Standalone mutating CLI sessions load plugins on each invocation.

### Plugin shape

Each script returns a table with required `name`, `can_handle`, and `enhance`
fields. `priority` is optional and defaults to zero:

```lua
return {
  name = "example",
  priority = 50,                          -- optional; higher wins
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
  - `log.info(msg)` / `log.warn(msg)` use the host logger: stderr for CLI/serve,
    and the opt-in debug log in the local TUI

Loading and URL routing have a two-second execution ceiling. JSON conversion
rejects cycles and enforces depth, node, and byte budgets. Installed plugins
still require trust: the capability sandbox is not process-level memory isolation.
Scripts have a 256 KiB size cap and a 30-second enhancement ceiling; a shorter
caller deadline takes precedence. Manager-owned plugin requests share the
fetcher's HTTP client, timeout, and destination policy. The feed User-Agent is
set on feed requests, not injected automatically into plugin requests; plugins
can supply one through `http.get` headers.

## Network behavior

- Feed fetches send the configured User-Agent and conditional ETag/Last-Modified
  headers, and handle 304 Not Modified responses.
- Refreshes check each feed's `refresh_interval`; bulk refresh concurrency is
  bounded by `max_concurrent_refreshes`.
- There is **no per-host rate limiter or automatic Retry-After backoff**. A
  helper parses numeric Retry-After values, but fetching does not schedule
  retries from that result. HTTP 429/503 responses are reported as errors.

### Refresh failures

An HTTP `504` means a gateway on the feed request path returned a timeout;
this is different from a broken local IPC connection. Increasing fwrd's HTTP
timeout does not fix a gateway's own timeout.

Bulk refresh keeps successful updates even when other feeds fail. Local and
server-connected TUIs show completed-work and failure counts, then reload feed
metadata so the affected feeds have error badges. Aggregate error details include a
stable feed identifier and safe host label, not the feed URL's credentials or
query string. The CLI prints its summary before returning a failing exit status
for per-feed failures. Retry affected feeds later; fwrd does not automatically
retry these HTTP errors or delete previously stored articles.

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

```bash
# Check formatting/static analysis and reachable dependency vulnerabilities
make lint
make security
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

# Install to GOBIN, or GOPATH/bin when GOBIN is unset
make install

# Build Docker image
make docker-build

# Install pinned development tools under .tools/bin, then format and lint
make tools             # golangci-lint v2.12.2
make fmt
make lint              # also installs the pinned linter if missing

# Create a release with GoReleaser (CI recommended)
make release            # requires GoReleaser installed locally
make release-snapshot   # builds artifacts but does not publish
```

Pushing a version tag triggers [the release workflow](.github/workflows/release.yml).
Creating a tag locally does not publish a release.

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

If no specific player is found, fwrd falls back to the platform opener: `open`
on macOS, `xdg-open` on Linux, or the Windows URL handler (`start` in config,
implemented without a command shell). Only absolute HTTP(S) launch targets are
accepted; local files and custom URI schemes are rejected.
