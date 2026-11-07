package audit

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestRedactURL(t *testing.T) {
	for _, raw := range []string{
		"https://user:password@feed.example.org/rss?token=secret&api_key=secret#secret",
		"/rss?custom_provider_credential=secret",
		"/rss?secret",
		"/rss?token=%zzsecret",
		"https://user:password@%zz/rss?secret",
	} {
		got := RedactURL(raw)
		for _, forbidden := range []string{"user", "password", "secret", "token", "api_key", "credential"} {
			if strings.Contains(got, forbidden) {
				t.Fatalf("RedactURL leaked %q: %s", forbidden, got)
			}
		}
	}
	if got := RedactURL("https://feed.example.org/rss"); got != "https://feed.example.org/rss" {
		t.Fatalf("unexpected clean URL: %s", got)
	}
}

func TestAuditRedactsInboundAndOutbound(t *testing.T) {
	var buf bytes.Buffer
	logger := NewWriter(&buf)
	logger.Log(&Record{Dir: In, URL: "/feed?auth=secret"})
	request, err := http.NewRequest(http.MethodGet, "https://user:password@feed.example.org/rss?token=secret", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	transport := &RoundTripper{Log: logger, Base: stubRT{err: &url.Error{
		Op: "Get", URL: request.URL.String(), Err: errors.New("secret transport diagnostic"),
	}}}
	if _, err = transport.RoundTrip(request); err == nil { //nolint:bodyclose // nil response on synthetic error
		t.Fatal("expected transport error")
	}
	for _, forbidden := range []string{"user", "password", "token", "secret"} {
		if strings.Contains(buf.String(), forbidden) {
			t.Fatalf("audit leaked %q: %s", forbidden, buf.String())
		}
	}
}

func TestSafeHTTPErrorPreservesCancellation(t *testing.T) {
	err := SafeHTTPError(&url.Error{Op: "Get", URL: "http://user:password@host/?secret", Err: context.Canceled})
	if !errors.Is(err, context.Canceled) || err.Error() != "HTTP request canceled" {
		t.Fatalf("unexpected safe error: %v", err)
	}
}
