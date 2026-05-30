package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/topics"
)

// seedCorpus stores one feed and a set of articles that form a clear
// topical cluster (three "kubernetes" pieces) plus an outlier, so the
// front page produces a lead, a section, and a catch-all.
func seedCorpus(t *testing.T, store *storage.Store) {
	t.Helper()
	if err := store.SaveFeed(&storage.Feed{ID: "f1", URL: "http://ex.com/feed", Title: "Example Blog"}); err != nil {
		t.Fatalf("SaveFeed: %v", err)
	}
	base := time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC)
	arts := []*storage.Article{
		{ID: "f1:1", FeedID: "f1", Title: "Kubernetes scheduling internals", Description: "How the kubernetes scheduler places pods", Published: base.AddDate(0, 0, 5)},
		{ID: "f1:2", FeedID: "f1", Title: "Debugging kubernetes networking", Description: "kubernetes pods and CNI plugins", Published: base.AddDate(0, 0, 4)},
		{ID: "f1:3", FeedID: "f1", Title: "Kubernetes operators in practice", Description: "Writing a kubernetes operator controller", Published: base.AddDate(0, 0, 3)},
		{ID: "f1:4", FeedID: "f1", Title: "A recipe for sourdough", Description: "Flour water salt and time", Published: base.AddDate(0, 0, 2)},
	}
	if err := store.SaveArticles(arts); err != nil {
		t.Fatalf("SaveArticles: %v", err)
	}
}

func TestFrontPageRenders(t *testing.T) {
	srv, store := newTestServer(t)
	seedCorpus(t, store)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"nameplate", "Kubernetes scheduling internals", "Example Blog", "/topic/"} {
		if !strings.Contains(body, want) {
			t.Errorf("front page missing %q", want)
		}
	}
}

func TestTopicPageRendersAndUnknown404(t *testing.T) {
	srv, store := newTestServer(t)
	seedCorpus(t, store)
	h := srv.Handler()

	// Discover the topic slug the way the handler builds it.
	arts, _ := store.GetArticles("", frontCorpus)
	model := topics.Build(arts, topics.DefaultOptions())
	var slug string
	for _, tp := range model.Topics {
		if len(tp.Terms) > 0 && tp.Terms[0] == "kubernetes" {
			slug = tp.Slug
		}
	}
	if slug == "" {
		t.Fatal("expected a kubernetes topic to form")
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/topic/"+slug, http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("topic status %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Kubernetes operators in practice") {
		t.Error("topic page missing one of its articles")
	}

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/topic/does-not-exist", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown topic status %d, want 404", rec.Code)
	}
}

func TestFeedsManagePage(t *testing.T) {
	srv, store := newTestServer(t)
	seedCorpus(t, store)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/feeds", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Refresh all", "Example Blog", "Export OPML"} {
		if !strings.Contains(body, want) {
			t.Errorf("/feeds missing %q", want)
		}
	}
}

func TestExcerptTrims(t *testing.T) {
	in := "<p>Hello <b>world</b>, this is a fairly long description that should be trimmed neatly.</p>"
	got := excerpt(in, 30)
	if strings.Contains(got, "<") {
		t.Errorf("excerpt left HTML tags: %q", got)
	}
	if len([]rune(got)) > 32 { // 30 + ellipsis slack
		t.Errorf("excerpt too long: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("expected ellipsis on trimmed excerpt: %q", got)
	}
}

func TestFeedLabelFallsBackOnDegenerateTitle(t *testing.T) {
	cases := []struct {
		name string
		feed storage.Feed
		want string
	}{
		{"all-digit title falls back to host", storage.Feed{Title: "04", URL: "https://qemu.org/feed.xml"}, "qemu.org"},
		{"whitespace title falls back to host", storage.Feed{Title: "  ", URL: "https://example.com/rss"}, "example.com"},
		{"short non-digit title kept", storage.Feed{Title: "Go", URL: "https://go.dev/blog/feed.atom"}, "Go"},
		{"normal title kept", storage.Feed{Title: "Phoronix", URL: "https://phoronix.com/rss.php"}, "Phoronix"},
		{"digit title, unparseable URL keeps title", storage.Feed{Title: "04", URL: "not a url"}, "04"},
	}
	for _, c := range cases {
		if got := feedLabel(&c.feed); got != c.want {
			t.Errorf("%s: feedLabel = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestExcerptDecodesEntities(t *testing.T) {
	in := "<p>everybody who&#39;s for it is too for it. &mdash; Daniel Jalkut &amp; friends</p>"
	got := excerpt(in, 200)
	if strings.Contains(got, "&mdash;") || strings.Contains(got, "&#39;") || strings.Contains(got, "&amp;") {
		t.Errorf("excerpt left raw HTML entities: %q", got)
	}
	for _, want := range []string{"—", "who's", "Jalkut & friends"} {
		if !strings.Contains(got, want) {
			t.Errorf("excerpt %q missing decoded %q", got, want)
		}
	}
}
