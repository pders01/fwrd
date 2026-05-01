package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/charmbracelet/glamour/styles"
	"golang.org/x/term"
)

// Theme preference values accepted by config.UI.Theme and the runtime
// toggle key. Stored as strings so they round-trip through TOML.
const (
	ThemePrefAuto  = "auto"
	ThemePrefLight = "light"
	ThemePrefDark  = "dark"
)

// resolveGlamourStyle picks the glamour style ("dark"/"light") for a
// given user preference. The flow is:
//
//  1. "light" / "dark" — explicit override, return immediately.
//  2. "auto" (or empty / unknown) — fall through to detection:
//     a. GLAMOUR_STYLE env var.
//     b. Non-TTY → NoTTYStyle (renders without ANSI).
//     c. COLORFGBG env var, when set by the terminal.
//     d. macOS only: read AppleInterfaceStyle from defaults; the key
//        exists only when Dark Appearance is active.
//     e. Default to dark (matches termenv's post-timeout fallback).
//
// We deliberately avoid OSC 11 background-color probes here because
// they can block startup for up to 5 s on terminals that never reply.
// The plist watcher handles the dynamic-detection job for macOS users.
func resolveGlamourStyle(pref string) string {
	switch strings.ToLower(strings.TrimSpace(pref)) {
	case ThemePrefLight:
		return styles.LightStyle
	case ThemePrefDark:
		return styles.DarkStyle
	}

	if s := os.Getenv("GLAMOUR_STYLE"); s != "" {
		return s
	}
	// COLORFGBG, if set, is an explicit signal from the terminal itself.
	// Honor it ahead of the TTY check so test environments (and edge
	// cases like piped stdout) still pick up the user's real terminal
	// background instead of falling through to NoTTY.
	if fgbg := os.Getenv("COLORFGBG"); strings.Contains(fgbg, ";") {
		parts := strings.Split(fgbg, ";")
		if bg, err := strconv.Atoi(parts[len(parts)-1]); err == nil {
			if bg < 7 || bg == 8 {
				return styles.DarkStyle
			}
			return styles.LightStyle
		}
	}
	if !term.IsTerminal(int(os.Stdout.Fd())) {
		return styles.NoTTYStyle
	}
	if runtime.GOOS == "darwin" {
		if isMacOSDarkAppearance() {
			return styles.DarkStyle
		}
		// On macOS we trust the AppleInterfaceStyle signal: the key is
		// absent for light mode, set to "Dark" for dark mode. Anything
		// else is light.
		return styles.LightStyle
	}
	return styles.DarkStyle
}

// isMacOSDarkAppearance returns true if AppleInterfaceStyle is set to
// "Dark" in the user's global defaults. The defaults binary exits 1
// when the key is absent (which is the macOS light-mode signal), so a
// non-zero exit is treated as "not dark" rather than an error.
func isMacOSDarkAppearance() bool {
	cmd := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle")
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(string(out)), "Dark")
}

// nextThemePref cycles auto → light → dark → auto.
func nextThemePref(cur string) string {
	switch strings.ToLower(strings.TrimSpace(cur)) {
	case ThemePrefAuto:
		return ThemePrefLight
	case ThemePrefLight:
		return ThemePrefDark
	default:
		return ThemePrefAuto
	}
}
