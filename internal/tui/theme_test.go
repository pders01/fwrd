package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/glamour/styles"
)

func TestResolveGlamourStyle_ExplicitPrefWins(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	t.Setenv("COLORFGBG", "0;15") // would normally select light

	if got := resolveGlamourStyle(ThemePrefDark); got != styles.DarkStyle {
		t.Errorf("dark pref: got %q want %q", got, styles.DarkStyle)
	}
	if got := resolveGlamourStyle(ThemePrefLight); got != styles.LightStyle {
		t.Errorf("light pref: got %q want %q", got, styles.LightStyle)
	}
}

func TestResolveGlamourStyle_PrefIsCaseInsensitive(t *testing.T) {
	for _, in := range []string{"LIGHT", "Light", "  light  "} {
		if got := resolveGlamourStyle(in); got != styles.LightStyle {
			t.Errorf("input %q: got %q want %q", in, got, styles.LightStyle)
		}
	}
}

func TestResolveGlamourStyle_AutoHonorsGlamourStyleEnv(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "ascii")
	t.Setenv("COLORFGBG", "")
	if got := resolveGlamourStyle(ThemePrefAuto); got != "ascii" {
		t.Errorf("got %q want %q", got, "ascii")
	}
}

func TestResolveGlamourStyle_AutoHonorsCOLORFGBG(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")

	// xterm/rxvt convention: trailing field 0–6 or 8 = dark, others = light.
	cases := []struct {
		fgbg string
		want string
	}{
		{"15;0", styles.DarkStyle},
		{"0;15", styles.LightStyle},
		{"7;8", styles.DarkStyle},
		{"15;7", styles.LightStyle},
	}
	for _, tc := range cases {
		t.Run(tc.fgbg, func(t *testing.T) {
			t.Setenv("COLORFGBG", tc.fgbg)
			if got := resolveGlamourStyle(ThemePrefAuto); got != tc.want {
				t.Errorf("COLORFGBG=%q got %q want %q", tc.fgbg, got, tc.want)
			}
		})
	}
}

func TestResolveGlamourStyle_UnknownPrefFallsThrough(t *testing.T) {
	// An empty or unrecognized preference must behave like "auto" rather
	// than blow up — older config files predate the field entirely.
	t.Setenv("GLAMOUR_STYLE", "ascii")
	t.Setenv("COLORFGBG", "")
	for _, in := range []string{"", "weird", "AUTO"} {
		if got := resolveGlamourStyle(in); got == "" {
			t.Errorf("input %q: got empty string", in)
		}
	}
}

func TestNextThemePref_Cycle(t *testing.T) {
	cases := []struct{ in, want string }{
		{ThemePrefAuto, ThemePrefLight},
		{ThemePrefLight, ThemePrefDark},
		{ThemePrefDark, ThemePrefAuto},
		{"", ThemePrefAuto},     // empty -> auto (default fallback)
		{"junk", ThemePrefAuto}, // unknown -> auto
		{"  Light  ", ThemePrefDark},
	}
	for _, tc := range cases {
		if got := nextThemePref(tc.in); got != tc.want {
			t.Errorf("next(%q) = %q want %q", tc.in, got, tc.want)
		}
	}
}

func TestMsgThemeApplied_Format(t *testing.T) {
	got := MsgThemeApplied("auto", styles.LightStyle)
	if !strings.Contains(got, "auto") || !strings.Contains(got, styles.LightStyle) {
		t.Errorf("auto label: got %q", got)
	}
	got = MsgThemeApplied("light", styles.LightStyle)
	if !strings.Contains(got, "light") {
		t.Errorf("light label: got %q", got)
	}
}
