package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/pders01/fwrd/internal/feed"
)

// writeRefreshSummary keeps completed work visible while retaining a failing
// exit status for per-feed errors. Setup failures have no meaningful summary.
func writeRefreshSummary(out io.Writer, summary feed.RefreshSummary, refreshErr error) error {
	if refreshErr == nil {
		refreshErr = errors.Join(summary.Errors...)
	}
	if refreshErr != nil && len(summary.Errors) == 0 {
		return fmt.Errorf("failed to refresh feeds: %w", refreshErr)
	}
	text := fmt.Sprintf("Refreshed %d feed(s), added %d article(s)", summary.UpdatedFeeds, summary.AddedArticles)
	if len(summary.Errors) > 0 {
		text += fmt.Sprintf("; %d failed", len(summary.Errors))
	}
	if _, err := fmt.Fprintln(out, text+"."); err != nil {
		return fmt.Errorf("writing refresh summary: %w", errors.Join(err, refreshErr))
	}
	if refreshErr != nil {
		return fmt.Errorf("failed to refresh feeds: %w", refreshErr)
	}
	return nil
}
