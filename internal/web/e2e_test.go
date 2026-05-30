package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

// e2eEnv is an end-to-end fixture: the real Server + Manager + store over a
// backend whose response can be switched between healthy, error, and hang at
// runtime, so a single test can add a feed cleanly and then drive the
// state-changing handlers through failure paths the way the browser would.
type e2eEnv struct {
	srv     *Server
	store   *storage.Store
	handler http.Handler
	backend *httptest.Server
	mode    atomic.Value // "ok" | "500" | "hang"
}

func newE2E(t *testing.T) *e2eEnv {
	t.Helper()
	env := &e2eEnv{}
	env.mode.Store("ok")

	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	env.store = store

	env.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		switch env.mode.Load().(string) {
		case "500":
			http.Error(w, "upstream boom", http.StatusInternalServerError)
		case "hang":
			time.Sleep(3 * time.Second)
			w.WriteHeader(http.StatusOK)
		default:
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(sampleRSS))
		}
	}))
	t.Cleanup(env.backend.Close)

	cfg := &config.Config{}
	cfg.Feed.HTTPTimeout = 1 * time.Second
	cfg.Feed.RefreshInterval = 0 // always re-fetch on refresh

	mgr := feed.NewManager(store, cfg)
	mgr.SetPermissiveValidation(true)

	srv, err := NewServer(store, mgr, nil, cfg)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	env.srv = srv
	env.handler = srv.Handler()
	return env
}

func (e *e2eEnv) setMode(m string) { e.mode.Store(m) }

// addFeed adds env.backend as a feed while the backend is healthy and returns
// the stored feed ID. It fails the test if the add does not succeed.
func (e *e2eEnv) addFeed(t *testing.T) string {
	t.Helper()
	e.setMode("ok")
	rec := postForm(t, e.handler, "/feeds", url.Values{"url": {e.backend.URL}}, true)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add feed: status %d, want 303: %s", rec.Code, rec.Body.String())
	}
	feeds, err := e.store.GetAllFeeds()
	if err != nil || len(feeds) == 0 {
		t.Fatalf("add feed: no feed stored: %v", err)
	}
	return feeds[0].ID
}

// assertGraceful reports a FE-unhandled-state failure when a state-changing
// action responds with a raw error page (any 5xx, or a 4xx that is not a
// deliberate redirect) instead of redirecting back to a usable page. The
// contract under test: a POST action either 303-redirects (PRG) or returns a
// page the browser can show — never a bare 5xx with the upstream error text.
func assertGraceful(t *testing.T, label string, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code >= 500 {
		t.Errorf("[%s] UNHANDLED: status %d, body %q — raw error page replaces the UI",
			label, rec.Code, strings.TrimSpace(rec.Body.String()))
		return
	}
	if rec.Code == http.StatusSeeOther {
		return // PRG redirect: graceful
	}
	t.Errorf("[%s] status %d (want 303 redirect), body %q",
		label, rec.Code, strings.TrimSpace(rec.Body.String()))
}

// flashFromRec decodes the one-shot flash cookie a handler set on its
// redirect response, mirroring what the browser carries to the next page.
// Returns nil when no (live) flash cookie was set.
func flashFromRec(t *testing.T, rec *httptest.ResponseRecorder) *FlashMsg {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name != flashCookie || c.MaxAge < 0 || c.Value == "" {
			continue
		}
		kindRaw, textRaw, ok := strings.Cut(c.Value, ".")
		if !ok {
			t.Fatalf("malformed flash cookie %q", c.Value)
		}
		kind, _ := url.QueryUnescape(kindRaw)
		text, _ := url.QueryUnescape(textRaw)
		return &FlashMsg{Kind: kind, Text: text}
	}
	return nil
}

// TestE2E_RefreshAll_PartialFailure is the reported bug: with one feed that
// 500s, refresh-all must not dump "failed to refresh feeds: ... HTTP error:
// 500" as a full page. Per-feed errors are persisted and badged on /feeds;
// the user gets a summary flash instead.
func TestE2E_RefreshAll_PartialFailure(t *testing.T) {
	env := newE2E(t)
	env.addFeed(t)

	env.setMode("500")
	rec := postForm(t, env.handler, "/refresh", url.Values{}, true)
	assertGraceful(t, "refresh-all w/ failing feed", rec)

	flash := flashFromRec(t, rec)
	if flash == nil || flash.Kind != flashError {
		t.Fatalf("expected an error flash summarizing the partial failure, got %+v", flash)
	}
	if !strings.Contains(flash.Text, "failed") {
		t.Errorf("flash %q should mention the failed feed(s)", flash.Text)
	}
}

// TestE2E_RefreshAll_AllHealthy: a clean refresh reports a success notice and
// no error.
func TestE2E_RefreshAll_AllHealthy(t *testing.T) {
	env := newE2E(t)
	env.addFeed(t)

	rec := postForm(t, env.handler, "/refresh", url.Values{}, true)
	assertGraceful(t, "refresh-all healthy", rec)
	if flash := flashFromRec(t, rec); flash == nil || flash.Kind != flashNotice {
		t.Fatalf("expected a success notice, got %+v", flash)
	}
}

// TestE2E_FlashRoundTrip: a flash set on the redirect is rendered on the
// landing page exactly once, then cleared (a reload shows nothing).
func TestE2E_FlashRoundTrip(t *testing.T) {
	env := newE2E(t)
	env.setMode("500")
	rec := postForm(t, env.handler, "/feeds", url.Values{"url": {env.backend.URL}}, true)

	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == flashCookie && c.MaxAge >= 0 {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("add-feed failure set no flash cookie")
	}

	// First landing render shows the flash and clears the cookie.
	req := httptest.NewRequest(http.MethodGet, "/feeds", http.NoBody)
	req.AddCookie(cookie)
	rec1 := httptest.NewRecorder()
	env.handler.ServeHTTP(rec1, req)
	if !strings.Contains(rec1.Body.String(), "Couldn&#39;t add") {
		t.Errorf("flash text not rendered on landing page; body=%q", rec1.Body.String())
	}
	cleared := false
	for _, c := range rec1.Result().Cookies() {
		if c.Name == flashCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("landing render did not clear the flash cookie")
	}
}

// TestE2E_RefreshSingle_Failure: refreshing one unreachable/erroring feed
// should land the user back on a page, not a raw 502.
func TestE2E_RefreshSingle_Failure(t *testing.T) {
	env := newE2E(t)
	id := env.addFeed(t)

	env.setMode("500")
	rec := postForm(t, env.handler, "/feeds/"+id+"/refresh", url.Values{}, true)
	assertGraceful(t, "refresh-single failing", rec)
}

// TestE2E_AddFeed_Unreachable: adding a feed whose server 500s should report
// the failure inline, not as a raw 502 page.
func TestE2E_AddFeed_Unreachable(t *testing.T) {
	env := newE2E(t)
	env.setMode("500")
	rec := postForm(t, env.handler, "/feeds", url.Values{"url": {env.backend.URL}}, true)
	assertGraceful(t, "add-feed 500", rec)
}

// TestE2E_AddFeed_EmptyURL: an empty URL is user error; it should re-render
// the feeds page with a message, not a bare 400 text page.
func TestE2E_AddFeed_EmptyURL(t *testing.T) {
	env := newE2E(t)
	rec := postForm(t, env.handler, "/feeds", url.Values{"url": {""}}, true)
	assertGraceful(t, "add-feed empty url", rec)
}

// TestE2E_OPMLImport_Malformed: a malformed OPML upload should not dump a raw
// 400 page.
func TestE2E_OPMLImport_Malformed(t *testing.T) {
	env := newE2E(t)
	rec := postMultipart(t, env.handler, "/opml/import", "file", "bad.opml", "this is not xml <<<")
	assertGraceful(t, "opml import malformed", rec)
}

// TestE2E_DeleteFeed_Nonexistent: deleting an unknown feed should still
// redirect, not error.
func TestE2E_DeleteFeed_Nonexistent(t *testing.T) {
	env := newE2E(t)
	rec := postForm(t, env.handler, "/feeds/does-not-exist/delete", url.Values{}, true)
	assertGraceful(t, "delete nonexistent feed", rec)
}
