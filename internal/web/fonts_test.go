package web

import (
	"strings"
	"testing"
)

func TestResolveFont(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", systemSerif},
		{"serif", systemSerif},
		{"SERIF", systemSerif},
		{" sans ", systemSans},
		{"sans-serif", systemSans},
		{"mono", systemMono},
		{"monospace", systemMono},
		{`Iosevka, ui-monospace, monospace`, `Iosevka, ui-monospace, monospace`},
	}
	for _, c := range cases {
		if got := resolveFont(c.in); got != c.want {
			t.Errorf("resolveFont(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolveFontSanitizesCustom(t *testing.T) {
	got := resolveFont(`Georgia}; body{display:none`)
	for _, bad := range []string{"{", "}", ";"} {
		if strings.Contains(got, bad) {
			t.Errorf("resolveFont kept %q in %q", bad, got)
		}
	}
	if !strings.Contains(got, "Georgia") {
		t.Errorf("resolveFont dropped the legitimate family: %q", got)
	}
}
