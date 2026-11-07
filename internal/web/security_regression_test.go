package web

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/audit"
	"github.com/pders01/fwrd/internal/storage"
)

func TestMutationFormsRejectOversizedAndMalformedBodies(t *testing.T) {
	paths := []string{"/feeds", "/opml/import", "/feeds/feed1/refresh", "/feeds/feed1/desk", "/feeds/feed1/delete", "/refresh", "/read", "/star"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			s, store := newTestServer(t)
			feedID, articleID := seed(t, store)
			for _, tc := range []struct {
				name, contentType, body string
				status                  int
			}{
				{"oversized", "application/x-www-form-urlencoded", "id=" + strings.Repeat("x", opmlMaxUpload+1), http.StatusRequestEntityTooLarge},
				{"bad escape", "application/x-www-form-urlencoded", "id=" + url.QueryEscape(articleID) + "&read=1&desk=technology&broken=%zz", http.StatusBadRequest},
				{"bad multipart", "multipart/form-data; boundary=broken", "--broken\r\nContent-Disposition: form-data; name=\"id\"\r\n\r\n" + articleID, http.StatusBadRequest},
				{"bad content type", "multipart/form-data; boundary=\"", "", http.StatusBadRequest},
				{"unsupported content type", "application/json", "{}", http.StatusUnsupportedMediaType},
			} {
				t.Run(tc.name, func(t *testing.T) {
					r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(tc.body))
					// Exercise the streaming limit, not only Content-Length.
					r.ContentLength = -1
					r.Header.Set("Content-Type", tc.contentType)
					r.Header.Set("Origin", "http://"+r.Host)
					w := httptest.NewRecorder()
					s.Handler().ServeHTTP(w, r)
					if w.Code != tc.status {
						t.Fatalf("status = %d, want %d: %s", w.Code, tc.status, w.Body.String())
					}
				})
			}
			f, err := store.GetFeed(feedID)
			if err != nil || f.DeskID != "" {
				t.Fatalf("rejected form mutated/deleted feed: %+v, %v", f, err)
			}
			a, err := store.GetArticle(articleID)
			if err != nil || a.Read || a.Starred {
				t.Fatalf("rejected form mutated article: %+v, %v", a, err)
			}
		})
	}
}

func TestMutationFormDataRemainsSupported(t *testing.T) {
	s, store := newTestServer(t)
	_, id := seed(t, store)
	for _, path := range []string{"/read", "/star", "/feeds/feed1/desk"} {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		for key, value := range map[string]string{"id": id, "read": "1", "starred": "1", "desk": "technology", "return": "/feeds"} {
			if err := mw.WriteField(key, value); err != nil {
				t.Fatal(err)
			}
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("Origin", "http://"+r.Host)
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusSeeOther {
			t.Fatalf("%s: status %d: %s", path, w.Code, w.Body.String())
		}
	}
	a, err := store.GetArticle(id)
	if err != nil || !a.Read || !a.Starred {
		t.Fatalf("FormData toggles not applied: %+v, %v", a, err)
	}
	f, err := store.GetFeed("feed1")
	if err != nil || f.DeskID != "technology" {
		t.Fatalf("FormData desk not applied: %+v, %v", f, err)
	}
}

func TestMutationMultipartStreamingLimit(t *testing.T) {
	s, _ := newTestServer(t)
	for _, path := range []string{"/read", "/opml/import"} {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		field, err := mw.CreateFormField("id")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(field, strings.Repeat("x", opmlMaxUpload+1)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, path, &body)
		r.ContentLength = -1
		r.Header.Set("Content-Type", mw.FormDataContentType())
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("%s: status = %d, want 413: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestSameOriginSchemeAndAuthority(t *testing.T) {
	for _, tc := range []struct {
		origin, host string
		tls, want    bool
	}{
		{"http://example.com", "example.com", true, false},
		{"https://example.com", "example.com", false, false},
		{"https://example.com", "example.com", true, true},
		{"https://EXAMPLE.com:443", "example.com", true, true},
		{"http://example.com:80", "EXAMPLE.com", false, true},
		{"https://example.com:444", "example.com", true, false},
		{"https://other.example", "example.com", true, false},
		{"https://user@example.com", "example.com", true, false},
		{"//example.com", "example.com", false, false},
		{"null", "example.com", true, false},
		{"https://[::1]:443", "[::1]", true, true},
	} {
		for _, header := range []string{"Origin", "Referer"} {
			r := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/read", http.NoBody)
			if tc.tls {
				r.TLS = &tls.ConnectionState{}
			}
			r.Header.Set(header, tc.origin)
			if got := sameOrigin(r); got != tc.want {
				t.Errorf("%s %q -> %q (TLS=%v): got %v, want %v", header, tc.origin, tc.host, tc.tls, got, tc.want)
			}
		}
	}
	// Origin takes precedence over an otherwise valid Referer.
	r := httptest.NewRequest(http.MethodPost, "https://example.com/read", http.NoBody)
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Referer", "https://example.com/article")
	if sameOrigin(r) {
		t.Fatal("cross-scheme Origin accepted via Referer fallback")
	}
}

func TestSafeReturnRejectsBrowserExternalRedirects(t *testing.T) {
	for _, value := range []string{"https://evil.example", "//evil.example", `/\evil.example`, `/\\evil.example`, "/\t/evil.example", "/\n/evil.example", "/\r/evil.example", "/\x00evil", "/\x7fevil", "/%5cevil.example", "/%2f%2fevil.example", "/%09/evil.example", "/%zz"} {
		if got := safeReturn(value); got != "/" {
			t.Errorf("safeReturn(%q) = %q, want /", value, got)
		}
	}
	for _, value := range []string{"/feeds", "/article?id=feed%3Ahttps%3A%2F%2Fexample.com%2Fa%3Fx%3D1%26y%3D2", "/search?q=a+b"} {
		if got := safeReturn(value); got != value {
			t.Errorf("safeReturn(%q) = %q", value, got)
		}
	}
}

func TestArticleReturnIDsRoundTripThroughForms(t *testing.T) {
	s, store := newTestServer(t)
	_, originalID := seed(t, store)
	a, err := store.GetArticle(originalID)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = "feed1:https://example.com/a?x=1&y=two+three#fragment"
	if err := store.SaveArticles([]*storage.Article{a}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/article?id="+url.QueryEscape(a.ID), http.NoBody))
	returns := regexp.MustCompile(`name="return" value="([^"]*)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(returns) != 2 {
		t.Fatalf("expected both toggle return fields, got %d", len(returns))
	}
	for i, match := range returns {
		target := html.UnescapeString(match[1])
		u, err := url.Parse(target)
		if err != nil || u.Query().Get("id") != a.ID || u.Fragment != "" || len(u.Query()) != 1 {
			t.Fatalf("article ID not query encoded: %q (%v)", target, err)
		}
		path, field := "/read", "read"
		if i == 1 {
			path, field = "/star", "starred"
		}
		response := postForm(t, s.Handler(), path, url.Values{"id": {a.ID}, field: {"1"}, "return": {target}}, true)
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != target {
			t.Fatalf("toggle redirect lost ID: status %d, location %q", response.Code, response.Header().Get("Location"))
		}
	}
}

func TestInboundAuditRedactsQuerySecrets(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{audit: audit.NewWriter(&buf)}
	h := s.auditLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "private upstream error", http.StatusBadGateway)
	}))
	for _, target := range []string{"/article?id=feed%3Ahttps%3A%2F%2Fuser%3Apassword%40example.com%3Ftoken%3Dsecret", "/feeds?provider_token=secret&odd-secret-key", "/feeds?bad=%zz"} {
		buf.Reset()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, http.NoBody))
		var record audit.Record
		if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), "secret") || strings.Contains(buf.String(), "password") || strings.Contains(buf.String(), "private upstream error") || record.Err != "" || !strings.HasSuffix(record.URL, "?redacted") {
			t.Fatalf("sensitive inbound audit record: %s", buf.String())
		}
	}
}
