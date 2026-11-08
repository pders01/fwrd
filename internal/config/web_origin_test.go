package config

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestNormalizeWebExternalOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"": "", "HTTPS://EXAMPLE.COM:443/": "https://example.com",
		"http://LOCALHOST:80": "http://localhost", "https://example.com:8443": "https://example.com:8443",
		"http://127.0.0.1:8080/": "http://127.0.0.1:8080", "https://[::1]:443": "https://[::1]",
		"http://[2001:DB8::1]:08080": "http://[2001:db8::1]:8080",
	} {
		got, err := NormalizeWebExternalOrigin(raw)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{
		"null", "//example.com", "example.com", "https:", "https:example.com", "ftp://example.com",
		"https:///", "https://user@example.com", "https://user:pass@example.com", "https://@example.com",
		"https://example.com/path", "https://example.com//", "https://example.com/%2f", "https://example.com?",
		"https://example.com?q=1", "https://example.com#", "https://example.com#fragment",
		"https://example.com:0", "https://example.com:65536", "https://example.com:-1", "https://example.com:abc", "https://example.com:",
		"https://[::1]:", "https://[garbage]", "https://[fe80::1%25lo0]", "https://::1",
		"https://a.example https://b.example", "https://a.example,https://b.example", "https://a.example,b.example",
		" https://example.com", "https://example.com\n", "https://exam\tple.com", "https://example.com/\x00",
		"https://example.com/\x7f", "https://example.com/\u0085", "https://example.com\\evil", "https://%65xample.com",
		"https://.example.com", "https://example..com", "https://-example.com",
	} {
		t.Run(strconv.Quote(raw), func(t *testing.T) {
			if got, err := NormalizeWebExternalOrigin(raw); err == nil {
				t.Fatalf("accepted %q as %q", raw, got)
			}
			// Environment variables cannot contain NUL; use an escaped TOML
			// value for that case to exercise the loader too.
			body := ""
			if strings.ContainsRune(raw, 0) {
				t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", "")
				if err := os.Unsetenv("FWRD_WEB_EXTERNAL_ORIGIN"); err != nil {
					t.Fatal(err)
				}
				body = "[web]\nexternal_origin=\"https://example.com/\\u0000\"\n"
			} else {
				t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", raw)
			}
			if _, err := Load(configFixture(t, body)); err == nil {
				t.Fatalf("Load accepted %q", raw)
			}
		})
	}
}

func TestLoadWebExternalOrigin(t *testing.T) {
	t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", "")
	cfg, err := Load(configFixture(t, ""))
	if err != nil || cfg.Web.ExternalOrigin != "" {
		t.Fatalf("default: %+v, %v", cfg, err)
	}
	if unsetErr := os.Unsetenv("FWRD_WEB_EXTERNAL_ORIGIN"); unsetErr != nil {
		t.Fatal(unsetErr)
	}
	path := configFixture(t, "[web]\nexternal_origin='HTTPS://NEWS.EXAMPLE:443/'\n")
	cfg, err = Load(path)
	if err != nil || cfg.Web.ExternalOrigin != "https://news.example" {
		t.Fatalf("file: %+v, %v", cfg, err)
	}
	t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", "http://LOCALHOST:8080/")
	cfg, err = Load(path)
	if err != nil || cfg.Web.ExternalOrigin != "http://localhost:8080" {
		t.Fatalf("environment: %+v, %v", cfg, err)
	}
	t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", "")
	cfg, err = Load(path)
	if err != nil || cfg.Web.ExternalOrigin != "" {
		t.Fatalf("empty environment override: %+v, %v", cfg, err)
	}
	t.Setenv("FWRD_WEB_EXTERNAL_ORIGIN", "https://example.com/path")
	if _, err := Load(path); err == nil {
		t.Fatal("invalid environment override accepted")
	}
	if err := os.Unsetenv("FWRD_WEB_EXTERNAL_ORIGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(configFixture(t, "[web]\nexternal_origin='https://example.com/path'\n")); err == nil {
		t.Fatal("invalid TOML origin accepted")
	}
}
