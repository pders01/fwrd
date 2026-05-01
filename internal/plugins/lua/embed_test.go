package lua

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureDefaultsSeedsOnce(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "plugins")

	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("first run: %v", err)
	}

	for _, name := range []string{"reddit.lua", "youtube.lua"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s seeded: %v", name, err)
		}
	}

	// Second run: dir exists, must not overwrite.
	marker := filepath.Join(dir, "reddit.lua")
	if err := os.WriteFile(marker, []byte("-- user edit"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDefaults(dir); err != nil {
		t.Fatalf("second run: %v", err)
	}
	got, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "-- user edit" {
		t.Errorf("EnsureDefaults overwrote user-edited file: %q", got)
	}
}

func TestWriteIfAbsentSkipsExistingFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "x.lua")
	if err := os.WriteFile(dest, []byte("-- original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeIfAbsent(dest, []byte("-- replacement")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "-- original" {
		t.Errorf("writeIfAbsent overwrote existing file: %q", got)
	}
}

func TestWriteIfAbsentCreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "x.lua")
	if err := writeIfAbsent(dest, []byte("-- new")); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "-- new" {
		t.Errorf("writeIfAbsent did not create file: %q", got)
	}
}

func TestRedditBuiltinEnhances(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "plugins")
	if err := EnsureDefaults(tmp); err != nil {
		t.Fatal(err)
	}

	plugin, err := LoadFile(filepath.Join(tmp, "reddit.lua"), Bindings{})
	if err != nil {
		t.Fatalf("load reddit.lua: %v", err)
	}
	defer plugin.Close()

	if !plugin.CanHandle("https://www.reddit.com/r/golang/") {
		t.Fatal("reddit plugin should handle reddit.com/r/...")
	}
	if plugin.CanHandle("https://example.com") {
		t.Fatal("reddit plugin should not handle non-reddit URLs")
	}

	info, err := plugin.EnhanceFeed(context.Background(), "https://www.reddit.com/r/golang/", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.FeedURL != "https://www.reddit.com/r/golang.rss" {
		t.Errorf("feed url: %q", info.FeedURL)
	}
	if !strings.Contains(info.Title, "r/golang") {
		t.Errorf("title: %q", info.Title)
	}
	if info.Metadata["subreddit"] != "golang" {
		t.Errorf("metadata: %v", info.Metadata)
	}
}

// youtubeStubHTML returns the minimal HTML the youtube plugin's
// fetch_channel_id helper looks for: a single canonical link tag with
// channel_id=<id>.
func youtubeStubHTML(channelID string) string {
	return `<html><head>` +
		`<link rel="alternate" type="application/rss+xml" ` +
		`href="https://www.youtube.com/feeds/videos.xml?channel_id=` + channelID + `">` +
		`</head><body></body></html>`
}

func TestYouTubeBuiltinResolvesLegacyHandle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(youtubeStubHTML("UCresolved123")))
	}))
	defer srv.Close()

	tmp := filepath.Join(t.TempDir(), "plugins")
	if err := EnsureDefaults(tmp); err != nil {
		t.Fatal(err)
	}
	plugin, err := LoadFile(filepath.Join(tmp, "youtube.lua"), Bindings{HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close()

	// /c/<name> form: plugin fetches the original URL and reads the
	// channel_id from the canonical RSS link tag in the response.
	url := srv.URL + "/c/somecreator"
	info, err := plugin.EnhanceFeed(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.FeedURL, "channel_id=UCresolved123") {
		t.Errorf("legacy /c/ did not resolve to canonical feed: %q", info.FeedURL)
	}
	if info.Metadata["channel_id"] != "UCresolved123" {
		t.Errorf("channel_id metadata: %v", info.Metadata)
	}
}

func TestYouTubeBuiltinResolvesAtHandleFallback(t *testing.T) {
	// The @handle branch hits https://www.youtube.com/@handle directly,
	// which we can't redirect from inside Lua. Verify the no-network
	// fallback: the plugin returns the original URL with a handle title.
	tmp := filepath.Join(t.TempDir(), "plugins")
	if err := EnsureDefaults(tmp); err != nil {
		t.Fatal(err)
	}
	plugin, err := LoadFile(filepath.Join(tmp, "youtube.lua"), Bindings{
		// nil http client so the @handle resolve fails fast.
	})
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close()

	info, err := plugin.EnhanceFeed(context.Background(),
		"https://www.youtube.com/@somecreator", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.Title, "@somecreator") {
		t.Errorf("title missing handle: %q", info.Title)
	}
	if info.Metadata["channel_handle"] != "somecreator" {
		t.Errorf("channel_handle metadata: %v", info.Metadata)
	}
}

func TestYouTubeBuiltinDirectChannelID(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "plugins")
	if err := EnsureDefaults(tmp); err != nil {
		t.Fatal(err)
	}

	plugin, err := LoadFile(filepath.Join(tmp, "youtube.lua"), Bindings{})
	if err != nil {
		t.Fatalf("load youtube.lua: %v", err)
	}
	defer plugin.Close()

	const url = "https://www.youtube.com/channel/UCabcdef123456"
	if !plugin.CanHandle(url) {
		t.Fatal("youtube plugin should handle channel URL")
	}

	info, err := plugin.EnhanceFeed(context.Background(), url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(info.FeedURL, "channel_id=UCabcdef123456") {
		t.Errorf("feed url: %q", info.FeedURL)
	}
	if info.Metadata["channel_id"] != "UCabcdef123456" {
		t.Errorf("metadata: %v", info.Metadata)
	}
}
