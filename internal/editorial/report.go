package editorial

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"
	"time"
)

// DefaultCandidateLimit is the wider article window callers should load
// before Build applies diversity caps to the final edition.
const DefaultCandidateLimit = 2000

// DiagnosticPolicy records the explicit composition limits used for a report.
type DiagnosticPolicy struct {
	MaxArticles        int `json:"max_articles"`
	MaxPerSource       int `json:"max_per_source"`
	MaxPerDesk         int `json:"max_per_desk"`
	MaxPerSourceInDesk int `json:"max_per_source_in_desk"`
}

// DiagnosticLead is the compact, content-free lead explanation emitted by
// diagnostics. It intentionally mirrors every LeadScore component.
type DiagnosticLead struct {
	ID                string    `json:"id,omitempty"`
	Title             string    `json:"title,omitempty"`
	FeedID            string    `json:"feed_id,omitempty"`
	Published         time.Time `json:"published,omitzero"`
	RecencyPoints     int       `json:"recency_points"`
	TopicSources      int       `json:"topic_sources"`
	TopicSourcePoints int       `json:"topic_source_points"`
	DeskSources       int       `json:"desk_sources"`
	DeskSourcePoints  int       `json:"desk_source_points"`
	StarredPoints     int       `json:"starred_points"`
	Total             int       `json:"total"`
}

// DiagnosticReport is a stable, machine-readable view of composition. It
// excludes article bodies and can safely be attached to tuning notes.
type DiagnosticReport struct {
	Policy        DiagnosticPolicy  `json:"policy"`
	Candidates    int               `json:"candidates"`
	Selected      int               `json:"selected"`
	FallbackCount int               `json:"fallback_count"`
	FallbackShare float64           `json:"fallback_share"`
	Lead          DiagnosticLead    `json:"lead"`
	Sources       []Share           `json:"sources"`
	Desks         []Share           `json:"desks"`
	Topics        []TopicDiagnostic `json:"topics"`
}

// NewDiagnosticReport snapshots an edition and its effective policy.
func NewDiagnosticReport(edition *Edition, options *Options) DiagnosticReport {
	opts := normalizeOptions(options)
	report := DiagnosticReport{
		Policy: DiagnosticPolicy{
			MaxArticles:        opts.MaxArticles,
			MaxPerSource:       opts.MaxPerSource,
			MaxPerDesk:         opts.MaxPerDesk,
			MaxPerSourceInDesk: opts.MaxPerSourceInDesk,
		},
	}
	if edition == nil {
		return report
	}
	report.Candidates = edition.Diagnostics.Candidates
	report.Selected = edition.Diagnostics.Selected
	report.FallbackCount = edition.Diagnostics.FallbackCount
	report.FallbackShare = edition.Diagnostics.FallbackShare
	report.Sources = append([]Share{}, edition.Diagnostics.Sources...)
	report.Desks = append([]Share{}, edition.Diagnostics.Desks...)
	report.Topics = append([]TopicDiagnostic{}, edition.Diagnostics.Topics...)
	if article := edition.LeadScore.Article; article != nil {
		report.Lead = DiagnosticLead{
			ID: article.ID, Title: article.Title, FeedID: article.FeedID,
			Published:         article.Published,
			RecencyPoints:     edition.LeadScore.RecencyPoints,
			TopicSources:      edition.LeadScore.TopicSources,
			TopicSourcePoints: edition.LeadScore.TopicSourcePoints,
			DeskSources:       edition.LeadScore.DeskSources,
			DeskSourcePoints:  edition.LeadScore.DeskSourcePoints,
			StarredPoints:     edition.LeadScore.StarredPoints,
			Total:             edition.LeadScore.Total,
		}
	}
	return report
}

// WriteDiagnostics writes either an inspectable text report or indented JSON.
func WriteDiagnostics(w io.Writer, edition *Edition, options *Options, jsonOutput bool) error {
	report := NewDiagnosticReport(edition, options)
	if jsonOutput {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(report)
	}

	if _, err := fmt.Fprintf(w, "Edition: %d selected from %d candidates\n", report.Selected, report.Candidates); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w,
		"Policy: source <= %d, desk <= %d, source/desk <= %d, edition <= %d\n",
		report.Policy.MaxPerSource, report.Policy.MaxPerDesk,
		report.Policy.MaxPerSourceInDesk, report.Policy.MaxArticles); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Fallback: %d (%.1f%%)\n", report.FallbackCount, 100*report.FallbackShare); err != nil {
		return err
	}
	if report.Lead.ID == "" {
		if _, err := fmt.Fprintln(w, "Lead: none (no validly dated candidate)"); err != nil {
			return err
		}
	} else if _, err := fmt.Fprintf(w,
		"Lead: %s [score=%d recency=%d topic=%d/%d sources desk=%d/%d sources starred=%d]\n",
		report.Lead.Title, report.Lead.Total, report.Lead.RecencyPoints,
		report.Lead.TopicSourcePoints, report.Lead.TopicSources,
		report.Lead.DeskSourcePoints, report.Lead.DeskSources, report.Lead.StarredPoints); err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "\nDESKS\tSTORIES\tSHARE"); err != nil {
		return err
	}
	for _, desk := range report.Desks {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%.1f%%\n", desk.Label, desk.Count, 100*desk.Share); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(tw, "\nSOURCES\tSTORIES\tSHARE"); err != nil {
		return err
	}
	for _, source := range report.Sources {
		if _, err := fmt.Fprintf(tw, "%s\t%d\t%.1f%%\n", source.Label, source.Count, 100*source.Share); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(tw, "\nTOPICS\tDESK\tSTORIES\tSOURCES"); err != nil {
		return err
	}
	for _, topic := range report.Topics {
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%d\t%d\n", topic.Label, topic.DeskID, topic.Articles, topic.SourceCount); err != nil {
			return err
		}
	}
	return tw.Flush()
}
