package tui

import "time"

type View int

const (
	ViewFeeds View = iota
	ViewArticles
	ViewReader
	ViewAddFeed
	ViewDeleteConfirm
	ViewRenameFeed
	ViewSearch
	ViewMedia
)

// UI timing and behavior constants
const (
	// Status display timing
	DefaultStatusDuration = 500 * time.Millisecond // Maximum duration for status messages

	// UI dimensions and spacing
	MinReadableWidth      = 40  // Minimum width for readable content
	MaxReadableWidth      = 120 // Maximum width for optimal readability
	MinNarrowWidth        = 20  // Absolute minimum for narrow screens
	MinModalWidth         = 15  // Minimum modal width before using full width
	PreferredModalWidth   = 60  // Preferred modal width
	MinInputWidth         = 10  // Minimum input field width
	NarrowScreenThreshold = 50  // Screen width threshold for narrow screen mode

	// Renderer configuration
	RendererWidthTolerance = 10 // Width change tolerance before re-creating renderer

	// Article pagination
	DefaultArticleLimit = 50 // Default number of articles to load

	// defaultMaxDescriptionLength is the fallback used when articleItem
	// has no configured limit (typically only in tests).
	defaultMaxDescriptionLength = 80

	// Layout chrome — rows reserved for headers, status bars, and
	// padding around the various list views. Subtracted from the total
	// terminal height so the inner list/viewport gets the remaining
	// space.
	listViewChrome      = 5  // feed and article list views
	searchViewChrome    = 10 // search view (input + status + results)
	viewportChrome      = 3  // reader, media list (single header + status)
	minSearchListHeight = 5  // floor when the terminal is very short

	// defaultSearchResultLimit caps how many results a single search
	// query returns to the UI.
	defaultSearchResultLimit = 20

	// searchResultDescLength caps the truncated description shown on
	// each search result row in the result list.
	searchResultDescLength = 50
)

// pickPositive returns v if positive, otherwise fallback. Used for
// config values that must be > 0 to make sense (limits, debounces).
func pickPositive(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
