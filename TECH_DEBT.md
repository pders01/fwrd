# Technical Debt & TODOs

This document tracks architectural and maintenance work items for fwrd.

## Completed (recent)

- [x] Make Bleve the default full‑text search engine
- [x] Debounced search input with fallback from in‑article → global
- [x] Always render a status bar across all views
- [x] Add subtle spinner during refresh and article loading
- [x] Standardize headers (add/rename/delete) + muted subtitles
- [x] Centralize status messages/formatters

## Search Layer

- [ ] Delete hygiene: remove all article docs for a feed on delete (not just the feed doc)
- [ ] Surface reindex errors (startup) via status/logging instead of ignoring
- [ ] Add Close() to search engine to flush resources on exit (optional)
- [ ] Snippets: show highlighted fragments for search results (Bleve fragments)
- [ ] Promote Bleve tests to default (keep heavy tests behind a tag if needed)

## Storage Layer

- [ ] Avoid scanning all articles: add `articles_by_feed` index (IDs) or per‑feed buckets
- [ ] Consider a published‑date index for faster paging without full in‑memory sort

## TUI / UI

- [ ] Extract render helpers: `renderHeader(title, subtitle)`, `renderCenteredBox(content)`
- [ ] Move repeated styles to branding helpers; reduce ad‑hoc style chains
- [ ] Add a StatusManager (extend current status helpers) with severity (info/warn/error)
- [ ] Uniform header/subtitle truncation utilities for narrow terminals

## Error Handling / Observability

- [ ] Add consistent error message helpers with context (fetch, parse, index)
- [ ] Optional `-debug` flag writing a rotating log under `~/.fwrd/`

## Config / Paths

- [ ] Normalize/expand DB and index paths once during config load; share helpers
- [ ] Optional config override for search index path (advanced users)

## Performance / UX Polish

- [ ] Batch index updates across a refresh loop (single index.Batch)
- [ ] Add small highlighted snippets in search results for quick scanning

