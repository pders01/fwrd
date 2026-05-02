package config

import (
	"fmt"
	"sort"
	"strings"
)

// reservedTerminalKeys maps a normalized "modifier+key" combination to a
// human-readable reason it is reserved by the terminal or the TUI itself.
// Bindings that resolve to one of these will collide with built-in input
// handling (e.g. ctrl+m == Enter at the VT100 layer).
var reservedTerminalKeys = map[string]string{
	"ctrl+m": "ctrl+m is Enter at the terminal layer",
	"ctrl+i": "ctrl+i is Tab at the terminal layer",
	"ctrl+[": "ctrl+[ is Esc at the terminal layer",
	"ctrl+j": "ctrl+j is Newline at the terminal layer",
	"ctrl+c": "ctrl+c always quits",
}

// Warnings returns non-fatal issues with the loaded config. Callers
// should print these to stderr at startup; nothing here blocks running.
func Warnings(cfg *Config) []string {
	if cfg == nil {
		return nil
	}
	var out []string

	mod := strings.ToLower(strings.TrimSpace(cfg.Keys.Modifier))
	bindings := map[string]string{
		"quit":         cfg.Keys.Bindings.Quit,
		"search":       cfg.Keys.Bindings.Search,
		"new_feed":     cfg.Keys.Bindings.NewFeed,
		"rename_feed":  cfg.Keys.Bindings.RenameFeed,
		"delete_feed":  cfg.Keys.Bindings.DeleteFeed,
		"refresh":      cfg.Keys.Bindings.Refresh,
		"toggle_read":  cfg.Keys.Bindings.ToggleRead,
		"open_media":   cfg.Keys.Bindings.OpenMedia,
		"theme_toggle": cfg.Keys.Bindings.ThemeToggle,
		"back":         cfg.Keys.Bindings.Back,
	}

	// Stable iteration so warning order is deterministic.
	names := make([]string, 0, len(bindings))
	for n := range bindings {
		names = append(names, n)
	}
	sort.Strings(names)

	seen := map[string]string{}
	for _, name := range names {
		val := strings.ToLower(strings.TrimSpace(bindings[name]))
		if val == "" {
			continue
		}
		// "back" is bound to a literal key (e.g. "esc"), not modifier+key.
		combo := val
		if name != "back" && mod != "" {
			combo = mod + "+" + val
		}
		if reason, ok := reservedTerminalKeys[combo]; ok {
			out = append(out, fmt.Sprintf("keys.bindings.%s = %q resolves to %s — %s; pick a different key", name, bindings[name], combo, reason))
		}
		if other, dup := seen[combo]; dup {
			out = append(out, fmt.Sprintf("keys.bindings.%s and keys.bindings.%s both resolve to %s", other, name, combo))
		} else {
			seen[combo] = name
		}
	}

	return out
}
