package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExternalOriginCanonicalAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, external, host string
		headers              http.Header
		want                 bool
	}{
		{"canonical-default-port", "https://news.example", "NEWS.EXAMPLE:443", http.Header{"Origin": {"HTTPS://NEWS.EXAMPLE:443"}}, true},
		{"canonical-ipv6", "https://[2001:db8::1]", "[2001:DB8::1]:443", http.Header{"Origin": {"https://[2001:db8::1]"}}, true},
		{"http-public-origin", "http://news.example", "NEWS.EXAMPLE:80", http.Header{"Origin": {"http://news.example"}}, true},
		{"host-port-mismatch", "https://news.example", "news.example:8443", http.Header{"Origin": {"https://news.example"}}, false},
		{"origin-port-mismatch", "https://news.example", "news.example", http.Header{"Origin": {"https://news.example:8443"}}, false},
		{"origin-wins", "https://news.example", "news.example", http.Header{"Origin": {"https://news.example"}, "Referer": {"null"}}, true},
		{"malformed-referer", "https://news.example", "news.example", http.Header{"Referer": {"https://user@news.example/article"}}, false},
		{"null-referer", "https://news.example", "news.example", http.Header{"Referer": {"null"}}, false},
		{"duplicate-referer", "https://news.example", "news.example", http.Header{"Referer": {"https://news.example/", "https://news.example/"}}, false},
		{"missing-both", "https://news.example", "news.example", http.Header{}, true},
		{"host-cannot-be-url", "https://news.example", "news.example/", http.Header{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Native transport is deliberately HTTP: only explicit config can
			// establish HTTPS as the public origin behind a terminating proxy.
			r := httptest.NewRequest(http.MethodPost, "http://backend.invalid/read", http.NoBody)
			r.Host, r.Header = tc.host, tc.headers
			if got := sameOriginFor(r, tc.external); got != tc.want {
				t.Fatalf("sameOriginFor = %v, want %v", got, tc.want)
			}
		})
	}
}
