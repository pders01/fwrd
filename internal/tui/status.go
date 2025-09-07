package tui

import (
	"fmt"
	"strings"
)

// Canonical short status messages used across the app.
const (
	MsgRefreshing     = "Refreshing…"
	MsgAddingFeed     = "Adding feed…"
	MsgRenaming       = "Renaming…"
	MsgDeleting       = "Deleting…"
	MsgLoadingArticle = "Loading article…"
	MsgNoResults      = "No results"
	MsgFeedRenamed    = "Feed renamed"
	MsgFeedDeleted    = "Feed deleted"
)

func MsgAddedFeed(title string, count int) string {
	return fmt.Sprintf("Added feed '%s' (%d articles)", strings.TrimSpace(title), count)
}

func MsgResultsCount(n int) string {
	if n == 1 {
		return "1 result"
	}
	return fmt.Sprintf("%d results", n)
}

func MsgRefreshSummary(updatedFeeds, addedArticles, errors, docCount int) string {
	base := fmt.Sprintf("Refreshed: %d feeds • %d articles", updatedFeeds, addedArticles)
	if errors > 0 {
		base += fmt.Sprintf(" • %d errors", errors)
	}
	if docCount >= 0 {
		base += fmt.Sprintf(" • idx: %d docs", docCount)
	}
	return base
}
