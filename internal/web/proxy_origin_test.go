package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"

	"github.com/pders01/fwrd/internal/config"
)

func TestNewServerValidatesExternalOrigin(t *testing.T) {
	for _, origin := range []string{"null", "https:example.com", "//example.com", "ftp://example.com", "https://", "https://user@example.com", "https://example.com/path", "https://example.com?", "https://example.com#", "https://example.com:65536", "https://example.com:", "https://example.com https://evil.example", "https://example.com\n"} {
		cfg := &config.Config{Web: config.WebConfig{ExternalOrigin: origin}}
		if _, err := NewServer(nil, nil, nil, cfg, nil); err == nil {
			t.Errorf("NewServer accepted %q", origin)
		}
	}
	cfg := &config.Config{Web: config.WebConfig{ExternalOrigin: "HTTPS://EXAMPLE.COM:443/"}}
	s, err := NewServer(nil, nil, nil, cfg, nil)
	if err != nil || s.externalOrigin != "https://example.com" {
		t.Fatalf("canonical origin: %v, %v", s, err)
	}
	cfg.Web.ExternalOrigin = "https://evil.example"
	if s.externalOrigin != "https://example.com" {
		t.Fatal("server trust changed after construction")
	}
}

// Real TLS terminates at the proxy, while the application sees plain HTTP.
// NewSingleHostReverseProxy preserves the public Host, as required in deployment.
func TestTLSReverseProxyExternalOrigin(t *testing.T) {
	for _, configured := range []bool{false, true} {
		name := "native-default"
		if configured {
			name = "explicit-public-origin"
		}
		t.Run(name, func(t *testing.T) {
			_, store := newTestServer(t)
			_, articleID := seed(t, store)
			front := httptest.NewUnstartedServer(nil)
			t.Cleanup(front.Close)
			publicOrigin := "https://" + front.Listener.Addr().String()
			publicHost := front.Listener.Addr().String()
			cfg := &config.Config{Web: config.WebConfig{Auth: config.WebAuthConfig{Username: "reader", Password: "secret"}}}
			if configured {
				cfg.Web.ExternalOrigin = publicOrigin + "/"
			}
			s, err := NewServer(store, nil, nil, cfg, nil)
			if err != nil {
				t.Fatal(err)
			}
			h := s.Handler()
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.TLS != nil {
					t.Error("backend unexpectedly received TLS")
				}
				h.ServeHTTP(w, r)
			}))
			defer backend.Close()
			target, err := url.Parse(backend.URL)
			if err != nil {
				t.Fatal(err)
			}
			front.Config.Handler = httputil.NewSingleHostReverseProxy(target)
			front.StartTLS()
			client := front.Client()
			client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

			type requestCase struct {
				name, origin, referer, host string
				method                      string
				noAuth, enhanced, forwarded bool
				want                        int
			}
			accepted, plainOrigin := http.StatusForbidden, http.StatusSeeOther
			if configured {
				accepted, plainOrigin = http.StatusSeeOther, http.StatusForbidden
			}
			cases := []requestCase{
				{name: "browser-form", origin: publicOrigin, want: accepted},
				{name: "enhanced-fetch", origin: publicOrigin, enhanced: true, want: accepted},
				{name: "referer", referer: publicOrigin + "/article?id=1", want: accepted},
				{name: "nonbrowser", want: http.StatusSeeOther},
				{name: "cross-site", origin: "https://evil.example", want: http.StatusForbidden},
				{name: "plain-http-origin", origin: "http://" + publicHost, want: plainOrigin},
				{name: "null", origin: "null", referer: publicOrigin + "/", want: http.StatusForbidden},
				{name: "origin-precedence", origin: "https://evil.example", referer: publicOrigin + "/", want: http.StatusForbidden},
				{name: "malformed", origin: publicOrigin + "?x=1", want: http.StatusForbidden},
				{name: "multiple-origins", origin: publicOrigin + " https://evil.example", want: http.StatusForbidden},
				{name: "mismatched-host", origin: publicOrigin, host: "other.example", want: http.StatusForbidden},
				{name: "attacker-host-and-origin", origin: "https://evil.example", host: "evil.example", forwarded: true, want: http.StatusForbidden},
				{name: "forged-forwarded", origin: "https://evil.example", forwarded: true, want: http.StatusForbidden},
				{name: "forwarded-cannot-change-trust", origin: publicOrigin, forwarded: true, want: accepted},
				{name: "auth-before-csrf", origin: "https://evil.example", noAuth: true, want: http.StatusUnauthorized},
				{name: "get-with-cross-site-origin", method: http.MethodGet, origin: "https://evil.example", want: http.StatusOK},
				{name: "get-needs-auth", method: http.MethodGet, noAuth: true, want: http.StatusUnauthorized},
			}
			if configured {
				cases = append(cases, requestCase{name: "nonbrowser-mismatched-host", host: "other.example", want: http.StatusForbidden})
			} else {
				cases = append(cases, requestCase{name: "native-http-still-works", origin: "http://" + publicHost, want: http.StatusSeeOther})
			}
			for _, path := range []string{"/read", "/star"} {
				for _, tc := range cases {
					t.Run(path+"/"+tc.name, func(t *testing.T) {
						if err := store.MarkArticleRead(articleID, false); err != nil {
							t.Fatal(err)
						}
						if err := store.MarkArticleStarred(articleID, false); err != nil {
							t.Fatal(err)
						}
						form := url.Values{"id": {articleID}, "read": {"1"}, "starred": {"1"}, "return": {"//evil.example"}}
						method, requestPath := http.MethodPost, path
						if tc.method != "" {
							method, requestPath = tc.method, "/feeds"
						}
						req, err := http.NewRequest(method, front.URL+requestPath, strings.NewReader(form.Encode()))
						if err != nil {
							t.Fatal(err)
						}
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						if !tc.noAuth {
							req.SetBasicAuth("reader", "secret")
						}
						if tc.origin != "" {
							req.Header.Set("Origin", tc.origin)
						}
						if tc.referer != "" {
							req.Header.Set("Referer", tc.referer)
						}
						if tc.host != "" {
							req.Host = tc.host
						}
						if tc.enhanced {
							req.Header.Set("X-Requested-With", "fetch")
						}
						if tc.forwarded {
							req.Header.Set("Forwarded", `proto=https;host=evil.example`)
							req.Header.Set("X-Forwarded-Proto", "https")
							req.Header.Set("X-Forwarded-Host", "evil.example")
						}
						resp, err := client.Do(req)
						if err != nil {
							t.Fatal(err)
						}
						body, err := io.ReadAll(resp.Body)
						resp.Body.Close()
						if err != nil {
							t.Fatal(err)
						}
						if resp.StatusCode != tc.want {
							t.Fatalf("status %d, want %d: %s", resp.StatusCode, tc.want, body)
						}
						if resp.Header.Get("Access-Control-Allow-Origin") != "" {
							t.Fatal("unexpected CORS grant")
						}
						if tc.want == http.StatusSeeOther && resp.Header.Get("Location") != "/" {
							t.Fatal("external return redirect accepted")
						}
						if tc.want == http.StatusUnauthorized && resp.Header.Get("WWW-Authenticate") == "" {
							t.Fatal("missing Basic Auth challenge")
						}
						a, err := store.GetArticle(articleID)
						if err != nil {
							t.Fatal(err)
						}
						wantMutation := tc.want == http.StatusSeeOther
						if a.Read != (wantMutation && path == "/read") || a.Starred != (wantMutation && path == "/star") {
							t.Fatalf("unexpected stored state: read=%v starred=%v", a.Read, a.Starred)
						}
					})
				}
			}
		})
	}
}

func TestExternalOriginRejectsMalformedBrowserHeaders(t *testing.T) {
	for _, external := range []string{"", "https://example.com"} {
		for _, values := range [][]string{{""}, {"null"}, {"https://example.com", "https://evil.example"}, {"https://example.com/path"}, {"https://example.com#"}, {"https://example.com:65536"}} {
			r := httptest.NewRequest(http.MethodPost, "https://example.com/read", http.NoBody)
			r.Header["Origin"] = values
			r.Header.Set("Referer", "https://example.com/article")
			if sameOriginFor(r, external) {
				t.Errorf("external %q accepted Origin %q", external, values)
			}
		}
	}
}
