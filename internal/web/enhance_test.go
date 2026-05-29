package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/opml"
	"github.com/pders01/fwrd/internal/storage"
)

const sampleRSS = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0"><channel>
<title>Backend Feed</title>
<link>http://example.invalid/</link>
<description>d</description>
<item><title>First post</title><link>http://example.invalid/1</link><guid>http://example.invalid/1</guid></item>
</channel></rss>`

// newManagerServer wires a real feed.Manager over an httptest backend
// serving sampleRSS, so the add/refresh/import handlers exercise the same
// fetch path production uses. Permissive validation is required because the
// backend binds loopback, which the secure validator rejects.
func newManagerServer(t *testing.T) (*Server, *storage.Store, *httptest.Server) {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write([]byte(sampleRSS))
	}))
	t.Cleanup(backend.Close)

	cfg := &config.Config{}
	cfg.Feed.HTTPTimeout = 5 * time.Second
	cfg.Feed.RefreshInterval = 0 // always re-fetch on refresh

	mgr := feed.NewManager(store, cfg)
	mgr.SetPermissiveValidation(true)

	srv, err := NewServer(store, mgr, nil, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return srv, store, backend
}

func TestAddFeedViaManager(t *testing.T) {
	srv, store, backend := newManagerServer(t)
	h := srv.Handler()

	rec := postForm(t, h, "/feeds", url.Values{"url": {backend.URL}}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303: %s", rec.Code, rec.Body.String())
	}
	feeds, err := store.GetAllFeeds()
	if err != nil {
		t.Fatalf("GetAllFeeds: %v", err)
	}
	if len(feeds) != 1 || feeds[0].URL != backend.URL {
		t.Fatalf("expected one feed for %s, got %+v", backend.URL, feeds)
	}
}

func TestRefreshAllViaManager(t *testing.T) {
	srv, store, backend := newManagerServer(t)
	h := srv.Handler()

	// Seed via the same manager so there is something to refresh.
	if rec := postForm(t, h, "/feeds", url.Values{"url": {backend.URL}}, true); rec.Code != http.StatusSeeOther {
		t.Fatalf("add status %d, want 303", rec.Code)
	}

	rec := postForm(t, h, "/refresh", url.Values{}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("refresh status %d, want 303: %s", rec.Code, rec.Body.String())
	}
	feeds, _ := store.GetAllFeeds()
	if len(feeds) != 1 {
		t.Fatalf("want one feed after refresh, got %d", len(feeds))
	}
	arts, _ := store.GetArticles(feeds[0].ID, 0)
	if len(arts) == 0 {
		t.Error("expected at least one article after refresh")
	}
}

func TestRefreshFeedDisabledWhenNoManager(t *testing.T) {
	srv, store := newTestServer(t) // nil manager
	feedID, _ := seed(t, store)
	rec := postForm(t, srv.Handler(), "/feeds/"+feedID+"/refresh", url.Values{}, true)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 when manager nil", rec.Code)
	}
}

func TestToggleStar(t *testing.T) {
	srv, store := newTestServer(t)
	_, articleID := seed(t, store)
	h := srv.Handler()

	rec := postForm(t, h, "/star", url.Values{
		"id":      {articleID},
		"starred": {"1"},
		"return":  {"/article?id=" + articleID},
	}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303", rec.Code)
	}
	got, err := store.GetArticle(articleID)
	if err != nil {
		t.Fatalf("GetArticle: %v", err)
	}
	if !got.Starred {
		t.Fatal("article should be starred")
	}

	// Unstar again.
	rec = postForm(t, h, "/star", url.Values{"id": {articleID}, "starred": {"0"}}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("unstar status %d, want 303", rec.Code)
	}
	got, _ = store.GetArticle(articleID)
	if got.Starred {
		t.Error("article should be unstarred")
	}
}

func TestStarCrossOriginRejected(t *testing.T) {
	srv, store := newTestServer(t)
	_, articleID := seed(t, store)
	rec := postForm(t, srv.Handler(), "/star", url.Values{"id": {articleID}, "starred": {"1"}}, false)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 for cross-origin star", rec.Code)
	}
}

func TestOPMLExport(t *testing.T) {
	srv, store := newTestServer(t)
	seed(t, store) // feed "Example" at http://example.com/feed
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/opml/export", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want attachment", cd)
	}
	feeds, err := opml.Parse(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("re-parse export: %v", err)
	}
	if len(feeds) != 1 || feeds[0].URL != "http://example.com/feed" {
		t.Errorf("exported OPML = %+v, want the seeded feed", feeds)
	}
}

// postMultipart uploads an OPML file in a multipart form with a same-origin
// Origin so the guard admits it.
func postMultipart(t *testing.T, h http.Handler, path, fieldName, fileName, content string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		t.Fatalf("write field: %v", err)
	}
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Origin", "http://"+req.Host)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestOPMLImportViaUpload(t *testing.T) {
	srv, store, backend := newManagerServer(t)
	h := srv.Handler()

	doc := `<?xml version="1.0"?><opml version="2.0"><body>` +
		`<outline type="rss" text="Backend" xmlUrl="` + backend.URL + `"/>` +
		`</body></opml>`

	rec := postMultipart(t, h, "/opml/import", "file", "feeds.opml", doc)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status %d, want 303: %s", rec.Code, rec.Body.String())
	}
	feeds, _ := store.GetAllFeeds()
	if len(feeds) != 1 || feeds[0].URL != backend.URL {
		t.Fatalf("import should add the backend feed, got %+v", feeds)
	}
}

func TestOPMLImportDisabledWhenNoManager(t *testing.T) {
	srv, _ := newTestServer(t) // nil manager
	doc := `<opml version="2.0"><body><outline type="rss" xmlUrl="http://x/f"/></body></opml>`
	rec := postMultipart(t, srv.Handler(), "/opml/import", "file", "f.opml", doc)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status %d, want 503 when manager nil", rec.Code)
	}
}

func newAuthServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := &config.Config{}
	cfg.Web.Auth.Username = "alice"
	cfg.Web.Auth.Password = "s3cret"
	srv, err := NewServer(store, nil, nil, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if !srv.AuthEnabled() {
		t.Fatal("AuthEnabled should be true when credentials are set")
	}
	return srv.Handler()
}

func TestBasicAuthRejectsMissingAndWrong(t *testing.T) {
	h := newAuthServer(t)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no creds: status %d, want 401", rec.Code)
	}
	if rec.Header().Get("WWW-Authenticate") == "" {
		t.Error("401 should carry a WWW-Authenticate challenge")
	}

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("alice", "wrong")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pass: status %d, want 401", rec.Code)
	}
}

func TestBasicAuthAcceptsCorrect(t *testing.T) {
	h := newAuthServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.SetBasicAuth("alice", "s3cret")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("valid creds: status %d, want 200", rec.Code)
	}
}
