package control

import (
	"errors"

	"github.com/pders01/fwrd/internal/feed"
)

// refreshSummaryWire keeps per-feed errors inside the result envelope. Error
// interface values cannot round-trip through JSON, so only their messages cross
// the wire; concrete error types and sentinel identity remain local.
type refreshSummaryWire struct {
	UpdatedFeeds  int
	AddedArticles int
	Errors        []string
}

func encodeRefreshSummary(summary feed.RefreshSummary) refreshSummaryWire {
	wire := refreshSummaryWire{
		UpdatedFeeds:  summary.UpdatedFeeds,
		AddedArticles: summary.AddedArticles,
	}
	if summary.Errors != nil {
		wire.Errors = make([]string, 0, len(summary.Errors))
		for _, err := range summary.Errors {
			if err != nil {
				wire.Errors = append(wire.Errors, err.Error())
			}
		}
	}
	return wire
}

func (wire refreshSummaryWire) decode() (feed.RefreshSummary, error) {
	summary := feed.RefreshSummary{
		UpdatedFeeds:  wire.UpdatedFeeds,
		AddedArticles: wire.AddedArticles,
	}
	if wire.Errors != nil {
		summary.Errors = make([]error, len(wire.Errors))
		for i, message := range wire.Errors {
			summary.Errors[i] = errors.New(message)
		}
	}
	return summary, errors.Join(summary.Errors...)
}
