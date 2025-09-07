# TODO - Future Improvements

This document tracks remaining improvement opportunities and optional enhancements for the fwrd RSS aggregator.

## Current Status

**Overall Test Coverage: 52.7%** (18 test files for 28 Go source files - 64.3% file coverage)

**Core modules have excellent coverage (89-90%):**
- ✅ **Validation**: 89.7% (comprehensive path, URL, and security validation tests)
- ✅ **Feed Management**: 90.2% (complete RSS/Atom parsing, fetching, and manager tests)
- ✅ **Configuration**: 89.5% (config loading, defaults, and path handling tests)
- ✅ **Debug Logging**: 82.2% (structured logging and level management tests)
- ✅ **Media**: 80.5% (type detection and launcher functionality tests)
- ✅ **Search**: 66.6% (Bleve engine and search functionality tests)
- ✅ **Storage**: 64.9% (database operations and indexing tests)

**Main testing gaps:**
- ⚠️ **TUI**: 25.2% (limited UI component testing - main opportunity)
- ⚠️ **CMD**: 16.1% (basic CLI command tests - mainly integration-tested)

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

- **All major technical debt has been resolved** ✅
- **Core business logic is well-tested** with 89-90% coverage
- **Codebase is production-ready** with comprehensive security and validation
- **Remaining items are optional enhancements** for even higher quality
- **TUI testing is the main remaining opportunity** for coverage improvement

The application is fully functional and well-tested in all critical areas. These TODO items represent opportunities for further polish and testing completeness.