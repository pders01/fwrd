# Technical Debt & TODOs

This document tracks architectural and maintenance work items for fwrd.

## Completed (recent)

- [x] Make Bleve the default full‑text search engine
- [x] Debounced search input with fallback from in‑article → global
- [x] Always render a status bar across all views
- [x] Add subtle spinner during refresh and article loading
- [x] Standardize headers (add/rename/delete) + muted subtitles
- [x] Centralize status messages/formatters
- [x] Add StatusKind severity for status/spinner styling
- [x] Promote Bleve tests to default (remove build tag)
- [x] Fix test stability (unique index path for :memory:, nil DB guards)
- [x] Index hygiene: delete all article docs on feed delete
- [x] Reindex errors surfaced (no silent ignore)
- [x] Storage perf: add `articles_by_feed` index; speed up `GetArticles` and `DeleteFeed`
- [x] Extract UI components: `renderHeader`, `renderCentered`, `renderInputFrame`, `renderMuted`, `renderHelp`
- [x] Add text truncation utilities: `truncateEnd`, `truncateMiddle` for narrow terminals
- [x] Optional `-debug` flag with simple file logger under `~/.fwrd/`
- [x] Bleve search engine improvements: Close(), snippets/fragments, batch updates
- [x] Add consistent error message helpers with context via `wrapErr`

## Search Layer

- [x] Delete hygiene: remove all article docs for a feed on delete
- [x] Surface reindex errors (startup) via error return
- [x] Add Close() to search engine to flush resources on exit (optional)
- [x] Snippets: show highlighted fragments for search results (Bleve fragments)

## Storage Layer

- [x] Avoid scanning all articles: add `articles_by_feed` index
- [ ] Consider a published‑date index for faster paging without full in‑memory sort

## TUI / UI

- [x] Extract UI components (functional style): `renderHeader(title, subtitle)`, `renderCentered(content)`, `renderInputFrame(...)`
- [ ] Move repeated styles to branding helpers; reduce ad‑hoc style chains
- [x] Add a StatusManager with severity (info/success/warn/error)
- [x] Uniform header/subtitle truncation utilities for narrow terminals

## Error Handling / Observability

- [x] Add consistent error message helpers with context (fetch, parse, index)
- [x] Optional `-debug` flag writing a rotating log under `~/.fwrd/`

## Config / Paths

- [ ] Normalize/expand DB and index paths once during config load; share helpers
- [ ] Optional config override for search index path (advanced users)

## Performance / UX Polish

- [x] Batch index updates across a refresh loop (single index.Batch)
- [x] Add small highlighted snippets in search results for quick scanning
