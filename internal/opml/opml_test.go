package opml

import (
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestExportParseRoundTrip(t *testing.T) {
	feeds := []*storage.Feed{
		{URL: "http://a.example/feed", Title: "Alpha"},
		{URL: "http://b.example/feed", Title: "Beta"},
	}
	data, err := Export(feeds, time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !strings.HasPrefix(string(data), "<?xml") {
		t.Error("export should begin with an XML declaration")
	}

	got, err := Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("round-trip recovered %d feeds, want 2", len(got))
	}
	if got[0].URL != "http://a.example/feed" || got[0].Title != "Alpha" {
		t.Errorf("first feed = %+v, want Alpha", got[0])
	}
}

func TestExportSkipsURLless(t *testing.T) {
	feeds := []*storage.Feed{
		{Title: "no url"},
		nil,
		{URL: "http://ok.example/feed", Title: "OK"},
	}
	data, err := Export(feeds, time.Time{})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	got, err := Parse(strings.NewReader(string(data)))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 1 || got[0].URL != "http://ok.example/feed" {
		t.Errorf("expected only the feed with a URL, got %+v", got)
	}
}

func TestParseNestedAndDeduped(t *testing.T) {
	const doc = `<?xml version="1.0"?>
<opml version="2.0">
<body>
  <outline text="Tech">
    <outline type="rss" text="One" xmlUrl="http://one.example/feed"/>
    <outline type="rss" title="Two" xmlUrl="http://two.example/feed"/>
  </outline>
  <outline type="rss" text="Dup" xmlUrl="http://one.example/feed"/>
  <outline text="empty category"/>
</body>
</opml>`
	got, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d feeds, want 2 (nested recovered, duplicate collapsed): %+v", len(got), got)
	}
	// title falls back to text when title attr is absent
	if got[0].Title != "One" {
		t.Errorf("first title = %q, want One", got[0].Title)
	}
	// title attr wins over text
	if got[1].Title != "Two" {
		t.Errorf("second title = %q, want Two", got[1].Title)
	}
}

func TestParseEmpty(t *testing.T) {
	got, err := Parse(strings.NewReader(`<opml version="2.0"><body></body></opml>`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected no feeds, got %+v", got)
	}
}

func TestParseInvalid(t *testing.T) {
	if _, err := Parse(strings.NewReader("not xml at all <<<")); err == nil {
		t.Error("expected an error parsing malformed OPML")
	}
}
