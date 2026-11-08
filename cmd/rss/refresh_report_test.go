package main

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/pders01/fwrd/internal/feed"
)

func TestWriteRefreshSummaryPreservesCompletedWorkAndFailure(t *testing.T) {
	upstream := errors.New("feed example.test: HTTP error: 504")
	setupErr := errors.New("cannot list feeds")
	for _, tc := range []struct {
		name    string
		summary feed.RefreshSummary
		err     error
		want    string
		wantErr error
	}{
		{name: "healthy", summary: feed.RefreshSummary{UpdatedFeeds: 2, AddedArticles: 3}, want: "Refreshed 2 feed(s), added 3 article(s).\n"},
		{name: "partial", summary: feed.RefreshSummary{UpdatedFeeds: 2, AddedArticles: 3, Errors: []error{upstream, upstream, upstream}}, err: errors.Join(upstream, upstream, upstream), want: "Refreshed 2 feed(s), added 3 article(s); 3 failed.\n", wantErr: upstream},
		{name: "all_failed", summary: feed.RefreshSummary{Errors: []error{upstream}}, err: upstream, want: "Refreshed 0 feed(s), added 0 article(s); 1 failed.\n", wantErr: upstream},
		{name: "failure_in_summary", summary: feed.RefreshSummary{Errors: []error{upstream}}, want: "Refreshed 0 feed(s), added 0 article(s); 1 failed.\n", wantErr: upstream},
		{name: "setup_failure", err: setupErr, wantErr: setupErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			err := writeRefreshSummary(&out, tc.summary, tc.err)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("error = %v, want %v", err, tc.wantErr)
			}
			if got := out.String(); got != tc.want {
				t.Fatalf("output = %q, want %q", got, tc.want)
			}
		})
	}
}

type failedRefreshSummaryWriter struct{}

func (failedRefreshSummaryWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestWriteRefreshSummaryPreservesOutputAndFetchErrors(t *testing.T) {
	upstream := errors.New("HTTP error: 504")
	err := writeRefreshSummary(failedRefreshSummaryWriter{}, feed.RefreshSummary{Errors: []error{upstream}}, upstream)
	if !errors.Is(err, io.ErrClosedPipe) || !errors.Is(err, upstream) {
		t.Fatalf("error = %v, want output and fetch causes", err)
	}
}
