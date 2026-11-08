package config

import (
	"net/url"
	"testing"
)

func FuzzNormalizeWebExternalOrigin(f *testing.F) {
	for _, seed := range []string{
		"", "https://example.org", "HTTP://LOCALHOST:080/",
		"https://[::1]:0443/", "https://user:password@example.org",
		"https://example.org/path", "https://example.org:0",
		"https://example.org#", "https://bad\r\nHost: evil",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		if len(raw) > 8192 {
			t.Skip()
		}
		canonical, err := NormalizeWebExternalOrigin(raw)
		if err != nil {
			return
		}
		again, err := NormalizeWebExternalOrigin(canonical)
		if err != nil || again != canonical {
			t.Fatalf("normalization is not idempotent: %q -> %q -> %q (%v)", raw, canonical, again, err)
		}
		if canonical == "" {
			if raw != "" {
				t.Fatalf("nonempty configuration silently disabled origin checks: %q", raw)
			}
			return
		}
		u, err := url.Parse(canonical)
		if err != nil || u.Host == "" || u.User != nil || u.Opaque != "" ||
			(u.Scheme != "http" && u.Scheme != "https") || u.Path != "" ||
			u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
			t.Fatalf("accepted a non-origin value: %q -> %q (%v)", raw, canonical, err)
		}
	})
}
