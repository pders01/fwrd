package tui

// IconSet holds glyphs used across the TUI. Two backends are supported:
// "nerd" assumes the user's terminal font is patched with Nerd Font glyphs
// (https://www.nerdfonts.com); "unicode" uses geometric Unicode that renders
// in any monospace font. An empty field means render the human-readable
// label without a leading glyph. Configured via UIConfig.Icons.
type IconSet struct {
	Error   string
	Search  string
	Article string
	Feed    string
	Video   string
	Image   string
	Audio   string
	PDF     string
	Unread  string
}

var nerdIcons = IconSet{
	Error:   "",
	Search:  "",
	Article: "",
	Feed:    "",
	Video:   "",
	Image:   "",
	Audio:   "",
	PDF:     "",
	Unread:  "",
}

var unicodeIcons = IconSet{
	Error:   "×",
	Search:  "",
	Article: "",
	Feed:    "■",
	Video:   "",
	Image:   "",
	Audio:   "",
	PDF:     "",
	Unread:  "●",
}

// NewIconSet returns the icon set for the given mode. Unknown modes fall
// back to the unicode set.
func NewIconSet(mode string) IconSet {
	if mode == "nerd" {
		return nerdIcons
	}
	return unicodeIcons
}

// withIcon prefixes name with glyph + space when glyph is non-empty,
// otherwise returns name unchanged.
func withIcon(glyph, name string) string {
	if glyph == "" {
		return name
	}
	return glyph + " " + name
}
