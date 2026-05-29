package web

import "strings"

// System font stacks. The leading ui-* generics resolve to the OS's own
// system font library (e.g. New York / San Francisco on macOS, Segoe on
// Windows) so nothing is bundled or fetched over the network.
const (
	systemSerif = `ui-serif, "New York", Georgia, Cambria, "Times New Roman", Times, serif`
	systemSans  = `ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif`
	systemMono  = `ui-monospace, SFMono-Regular, Menlo, Consolas, "Liberation Mono", monospace`
)

// resolveFont maps a configured WebConfig.Font value to a CSS font-family
// stack. The presets "serif"/"sans"/"mono" select a system stack; anything
// else is treated as a raw CSS font-family list and used verbatim (after
// sanitizing characters that could break out of the CSS declaration).
// Empty falls back to the readable serif default.
func resolveFont(font string) string {
	switch strings.ToLower(strings.TrimSpace(font)) {
	case "", "serif":
		return systemSerif
	case "sans", "sans-serif":
		return systemSans
	case "mono", "monospace":
		return systemMono
	default:
		return sanitizeFontValue(font)
	}
}

// sanitizeFontValue strips characters that would let a custom font value
// escape its CSS custom-property declaration. The config is user-owned, so
// this guards against accidental breakage rather than a hostile source.
// "/" and "*" go too, so a stray "/* */" can't open a comment that swallows
// the rest of the declaration.
func sanitizeFontValue(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case '{', '}', ';', '<', '>', '/', '*', '\\':
			return -1
		default:
			return r
		}
	}, s)
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return systemSerif
	}
	return cleaned
}
