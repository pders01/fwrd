package editorial

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestWriteDiagnosticsTextAndJSON(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	feeds := []*storage.Feed{
		{ID: "a", URL: "https://a.example/rss", DeskID: DeskWorld},
		{ID: "b", URL: "https://b.example/rss", DeskID: DeskWorld},
		{ID: "c", URL: "https://c.example/rss", DeskID: DeskWorld},
	}
	articles := []*storage.Article{
		article("a1", "a", "Election court ruling", now.Add(-time.Hour)),
		article("b1", "b", "Election court appeal", now.Add(-2*time.Hour)),
		article("c1", "c", "Election court hearing", now.Add(-3*time.Hour)),
	}
	opts := DefaultOptions()
	opts.Now = now
	edition := Build(articles, feeds, &opts)

	var text bytes.Buffer
	if err := WriteDiagnostics(&text, edition, &opts, false); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Edition: 3 selected", "Fallback: 0", "World", "SOURCES", "TOPICS", "topic="} {
		if !strings.Contains(text.String(), want) {
			t.Errorf("text report missing %q:\n%s", want, text.String())
		}
	}

	var encoded bytes.Buffer
	if err := WriteDiagnostics(&encoded, edition, &opts, true); err != nil {
		t.Fatal(err)
	}
	var report DiagnosticReport
	if err := json.Unmarshal(encoded.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON report: %v\n%s", err, encoded.String())
	}
	if report.Selected != 3 || report.Policy.MaxArticles != opts.MaxArticles {
		t.Fatalf("JSON report = %+v", report)
	}
	if report.Lead.ID == "" || report.Lead.TopicSources < 2 || report.Lead.TopicSourcePoints == 0 {
		t.Fatalf("JSON lead lacks support explanation: %+v", report.Lead)
	}
}
