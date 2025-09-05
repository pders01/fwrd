package tui

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

// Deprecated: legacy Model struct removed; use App in app.go.
