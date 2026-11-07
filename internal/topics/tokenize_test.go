package topics

import (
	"strings"
	"testing"
)

func TestStripURLsUnicodeOffsets(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"İİİ", "İİİ"}, // strings.ToLower shortens this UTF-8 string
		{"İ HTTP://example.test/x résumé", "İ   résumé"},
		{"日本語 https://example.test/é\u2003続き", "日本語  \u2003続き"},
		{"ẞ WWW.example.test/x<text>", "ẞ  <text>"},
		{"\xffİhttps://example.test", "\xffİ "},
	} {
		if got := stripURLs(tt.input); got != tt.want {
			t.Errorf("stripURLs(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func FuzzStripURLs(f *testing.F) {
	for _, seed := range []string{"İİİ", "İ https://example.test/é\u2003next", "ẞWWW.example.test", "\xffİhttp://x", "İİİİİİİİİİ"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := stripURLs(s)
		if len(got) > len(s) || stripURLs(got) != got {
			t.Fatalf("invalid result for %q: %q", s, got)
		}
		// ASCII scheme matching must not depend on Unicode case folding.
		if strings.Contains(strings.ToLower(got), "https://") {
			t.Fatalf("URL survived: %q", got)
		}
	})
}
