package feed

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestRefreshSummaryOnlyFeedErrors(t *testing.T) {
	first := errors.New("HTTP 504")
	second := fmt.Errorf("feed slow: %w", context.DeadlineExceeded)
	summary := RefreshSummary{Errors: []error{first, second}}
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"single", first, true},
		{"joined", errors.Join(first, second), true},
		{"wrapped", fmt.Errorf("refresh: %w", errors.Join(first, second)), true},
		{"same_message_unrelated", errors.New("HTTP 504"), false},
		{"top_level", errors.New("cannot list feeds"), false},
		{"additional_failure", errors.Join(first, errors.New("database unavailable")), false},
		{"per_feed_deadline", fmt.Errorf("another wrapping: %w", context.DeadlineExceeded), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summary.OnlyFeedErrors(tc.err); got != tc.want {
				t.Fatalf("OnlyFeedErrors(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
	if (RefreshSummary{}).OnlyFeedErrors(first) {
		t.Fatal("empty summary classified a failure as per-feed")
	}
}
