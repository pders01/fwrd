# Codebase audit remediation

The codebase audit was addressed in parallel ownership-separated workstreams.
The table maps the reported defects to implementation and regression coverage.
These changes are not a claim of formal verification or complete sandbox isolation.

## Security boundaries

| Finding | Implementation / regression coverage |
| --- | --- |
| DNS, redirect and plugin SSRF | Shared dial-time destination validation, all-answer checks, IP-pinned dialing, redirect checks and explicit intranet mode; validation HTTP and feed security tests |
| Silent-peer web listener starvation | Bounded concurrent TLS classification, iterative acceptance and shutdown ownership; TLS mux regression tests |
| Ignored environment authentication | Nested environment bindings, precedence tests, partial-credential rejection; config security tests |
| Unauthenticated service exposure | Loopback service defaults; non-loopback CLI serving requires credentials or explicit consent; CLI security tests |
| Vulnerable HTML dependency | x/net v0.62.0 and Go 1.26.10; pinned reachable-vulnerability scanning in CI |
| Multipart resource exhaustion | Body limits and explicit parsing on every mutation route; web security regression tests |
| Cross-scheme CSRF and external return redirects | Scheme/authority comparisons, safe return parsing and query-encoded article IDs; web security regression tests |
| Terminal control injection | Shared terminaltext sanitizer before styling, decoded-entity handling, safe CLI/log output; sanitizer/TUI/media tests and fuzz corpus |
| Plugin hangs, cyclic JSON and stale reloads | Context-aware admission, bounded entry points/conversion, path-owned replacement and cleanup; plugin security/reload tests |
| URL credentials in audit logs | Userinfo, query and fragment redaction plus safe HTTP errors; audit redaction tests |

## Integrity, concurrency and lifecycle

| Finding | Implementation / regression coverage |
| --- | --- |
| Metadata committed before articles | CommitFeed transaction includes validators/timestamps and merged articles; failed creation/refresh rollback tests |
| Duplicate subscriptions and unstable IDs | Atomic create-only commits, idempotent duplicate detection and deterministic GUID-less IDs; persistence and parser identity tests |
| Stale full-record desk/title writes | Atomic SetFeedDesk/RenameFeed operations, persistent title override, committed-record notifications; storage contracts and desk regression tests |
| Article feed reassignment corrupts indexes | Reassignment rejected transactionally through both backends; storage contract tests |
| Pre-epoch/undated ordering and expensive feed pagination | Full-range ordered keys, per-feed indexes and transactional rebuild; date/migration/allocation tests |
| Oversized IPC pages | Byte-budgeted server reads and adaptive client page reduction; bounded-page and RPC pagination tests |
| Cancelled IPC work executes later | Request deadlines and disconnect cancellation reach queued/running work; cancellation/deadline/pipelining tests |
| Unbounded IPC resources and unsafe socket removal | Connection/frame/dispatch budgets, response-write deadlines and positive stale-socket evidence; frame-budget/socket/RPC tests |
| Concurrent renderer and index batches | Exclusive renderer access, full batch-scope ownership and coordinated local operations; TUI/search concurrency tests |
| Partial/stale search and reader results | Authoritative search hydration, input snapshots and generation/target checks on completions/errors; search/TUI tests |
| Worker mutations and rapid read/star toggles | Per-field sequenced write intents; model updates only on Update; overlapping-intent tests |
| Index/theme/worker leaks | Cancellation-aware theme reader, worker drain and owned-index close; shutdown/index-reuse tests |
| Runtime TLS expiry | Handshake-time renewal; valid-certificate fallback on transient errors, no expired fallback; clock-controlled TLS tests |
| Config/index/service path hazards | Database-relative default indexes, file-relative config paths, private config replacement, non-overwriting generation, absolute service paths and systemd quoting; config/service tests |
| Incomplete quality gates | CLI tests included, macOS/Linux full tests, Windows native package tests, pinned lint and vulnerability scan; build targets compile the command package |

## Upgrade and operational notes

- Build with Go 1.26.10 or newer. Rebuild/reinstall and restart serve **and**
  clients together to activate the new operations and policies.
- Back up the database before upgrading. Derived indexes rebuild atomically on
  first open; this can take time for large stores. Corrupt or oversized persisted
  records fail the migration without modifying the original transaction.
  Downgrading to an older binary after migration is not supported.
- Service installation is now private by default. Set web authentication before
  binding to the LAN, or deliberately opt in with --allow-unauthenticated.
- Config generation refuses existing files. Saved configuration is private on
  Unix. Service environment variables must be configured with the service manager.
- Private feed access is explicit via feed.allow_private_networks. Outbound proxy
  environment variables are not honored under the destination policy.
- Already-committed database transactions cannot be rolled back by cancellation.
- Historical GUID-less duplicates are not automatically deleted. Legacy title
  changes without an explicit title override cannot be inferred safely.
- Installed plugins remain trusted extensions, not OS-isolated processes.
  Native Go plugins and explicitly injected HTTP clients are trusted capabilities.
- Old audit/log contents are not rewritten. Review previously recorded URL
  credentials/tokens and rotate them if warranted. Audit URL paths remain
  visible and can themselves contain sensitive identifiers; treat logs/backups
  as sensitive data. Log files are made private when opened on Unix.
- Local CA rotation eventually requires renewed client trust.

## Verification

Final integration checks on macOS/arm64 with Go 1.26.10:

- Full uncached `go test -race ./... -count=1`: passed.
- `go vet ./...`: passed.
- Pinned golangci-lint 2.12.2: zero issues.
- Pinned govulncheck 1.9.0: no vulnerabilities found.
- Short timed fuzz runs for URL stripping, terminal text, and Markdown input:
  passed; the discovered Markdown regression input is retained as a corpus seed.
- Cross-builds: Windows/amd64, Linux/amd64, Linux/armv7 passed.
- Deep-feed-page benchmark (10 iterations, Apple M2 Pro): approximately
  0.118 ms, 26 KB, 486 allocations per page. A regression test separately checks
  that allocations do not scale with total feed size.
- `git diff --check`: passed.

Native Windows/Linux execution, Docker image execution, and a manual interactive
TUI walkthrough are separate from cross-compilation and are not claimed by these
local checks. CI now includes full Linux/macOS tests and selected native Windows
platform tests.
