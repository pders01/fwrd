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

	// Database retry configuration
	MaxDatabaseRetries     = 3
	BaseDatabaseRetryDelay = 100 * time.Millisecond

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
)
