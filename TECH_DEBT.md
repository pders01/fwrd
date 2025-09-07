# Technical Debt & Code Quality Improvements

This document tracks code quality issues, technical debt, and improvement opportunities identified during comprehensive code review.

## High Priority Issues (Address Soon)

### Resource Management & Error Handling
- [ ] **Resource Cleanup Inconsistencies** (`cmd/rss/main.go:197-351`)
  - Fix inconsistent `store.Close()` patterns across CLI commands
  - Implement consistent defer pattern or error cleanup handling
  
- [ ] **Error Handling Anti-patterns** (`cmd/rss/main.go` - various `log.Fatal` calls)
  - Replace `log.Fatal()` with proper error propagation
  - Improve user experience with graceful error handling
  - Enable better testing by avoiding abrupt termination

- [ ] **Database Transaction Management** (`internal/storage/store.go:271-321`)
  - Add transaction support for complex operations like `DeleteFeed`
  - Implement proper rollback mechanisms for data integrity

### Concurrency & Performance
- [ ] **Concurrent Access Safety** (`internal/feed/manager.go:146-178`)
  - Review mutex locking strategy in `RefreshAllFeeds()`
  - Consider worker pools instead of unlimited goroutines
  - Prevent potential deadlocks under high concurrency

## Medium Priority Improvements

### Configuration & Infrastructure  
- [ ] **Configuration Path Handling** (`internal/config/config.go:216-242`)
  - Centralize path handling with comprehensive edge case coverage
  - Handle all environment-specific path scenarios
  
- [ ] **HTTP Client Configuration** (`internal/feed/fetcher.go:19-28`)
  - Add configurable timeouts and retry logic
  - Implement circuit breaker patterns for better resilience

### Search & Memory Management
- [ ] **Search Engine Fallback Strategy** (`internal/tui/app.go:155-159`)
  - Add logging when search engine initialization fails
  - Expose search engine type/status in UI
  
- [ ] **Memory Usage in Large Indexes** (`internal/search/bleve_engine.go:96-130`)
  - Implement streaming/chunked indexing for large datasets
  - Prevent OOM conditions with very large RSS collections

## Security Improvements

### Input Validation & Path Security
- [ ] **Feed URL Validation** (`internal/tui/keyhandler.go:508-514`)
  - Implement comprehensive URL validation beyond basic checks
  - Whitelist allowed URL schemes for security
  
- [ ] **File Path Security** (Configuration and database file handling)
  - Implement path sanitization and validation
  - Prevent directory traversal attacks
  - Validate user-provided file paths

### Content Security
- [ ] **Content Size Limits** (Article rendering and content handling)
  - Implement article content size limits
  - Add streaming for large content to prevent memory issues
  - Protect against malicious oversized content

## Low Priority Nice-to-Have Improvements

### Code Quality & Maintainability
- [ ] **Code Duplication** (`internal/tui/` UI rendering functions)
  - Create shared UI component library/helpers
  - Reduce maintenance burden and ensure consistent styling

- [ ] **Magic Numbers and Constants** (Throughout codebase)
  - Move hardcoded values to configuration or constants file
  - Make timeouts, buffer sizes, UI dimensions configurable

- [ ] **Logging Strategy** (`internal/debuglog/log.go`)
  - Implement structured logging with configurable levels
  - Add consistent logging patterns across modules
  - Improve debugging and monitoring capabilities

### Performance & Scalability
- [ ] **Database Index Strategy** (`internal/storage/store.go:29-41`)
  - Optimize custom date indexing implementation
  - Consider more efficient indexing strategies or pagination
  - Improve query performance as article count grows

- [ ] **Dependency Management** (`go.mod`)
  - Consider build tags for optional features (e.g., Bleve)
  - Reduce binary size and potential security surface area

## Testing & Coverage Improvements

### Test Coverage Expansion
- [ ] **Integration Test Coverage**
  - Add integration tests for complete TUI workflows
  - Test error recovery scenarios more thoroughly
  - Implement property-based tests for URL parsing

- [ ] **Performance Testing**
  - Add benchmark tests for search operations
  - Test memory usage patterns with large datasets
  - Profile concurrent refresh operations

- [ ] **Error Path Testing**
  - Increase coverage for error scenarios
  - Test resource cleanup in failure cases
  - Validate graceful degradation

## Implementation Patterns & Best Practices

### Recommended Helper Patterns

**Resource Management Helper:**
```go
func withStore(cfg *config.Config, fn func(*storage.Store) error) error {
    store, err := getStore(cfg)
    if err != nil {
        return err
    }
    defer store.Close()
    return fn(store)
}
```

**Error Propagation:**
```go
// Replace: log.Fatal(err)
// With: return fmt.Errorf("operation failed: %w", err)
```

**Concurrent Operations:**
```go
// Use bounded concurrency
const maxConcurrentRefresh = 5
semaphore := make(chan struct{}, maxConcurrentRefresh)
```

## Architecture Strengths (Maintain)

✅ **Well-Designed Patterns:**
- Clean separation of concerns (TUI, storage, feed management)
- Proper dependency injection patterns
- Interface-based design for search engines
- Modular configuration system
- Good input sanitization in search functionality
- No SQL injection vulnerabilities (using BoltDB)

## Priority Action Plan

1. **Phase 1 (Critical)**: Fix resource cleanup and error handling patterns
2. **Phase 2 (Important)**: Improve concurrency safety and transaction management  
3. **Phase 3 (Security)**: Enhance input validation and path security
4. **Phase 4 (Performance)**: Optimize memory usage and database operations
5. **Phase 5 (Quality)**: Improve logging, testing, and reduce code duplication

## Notes

- Current test coverage: ~37% file coverage (14 test files for 38 Go files)
- Codebase is production-ready with Phase 1-2 improvements
- Architecture shows thoughtful design decisions throughout
- Main focus should be on robustness and scalability improvements