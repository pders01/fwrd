package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

const AppName = "fwrd"

// ASCII art logo lines for fwrd - canonical definition
var LogoLines = []string{
	" ▄████ ▄     ▄▄▄▄▄▄   ▄████▄▄",
	"██▀    ██  ▄ ██   ▀██ ██   ▀██",
	"██▀▀▀▀ ██ ███ ██▀▀▀█ ██    ██",
	"██     ███████ ██   ██ ██   ██",
	"██      ██ ██  ██   ██ ███████",
}

// Legacy logo constant for backwards compatibility
const Logo = `
 ▄████ ▄     ▄▄▄▄▄▄   ▄████▄▄
██▀    ██  ▄ ██   ▀██ ██   ▀██
██▀▀▀▀ ██ ███ ██▀▀▀█ ██    ██
██     ███████ ██   ██ ██   ██
██      ██ ██  ██   ██ ███████
`

const CompactLogo = `fwrd ›`

// Banner gradient colors
var BannerColors = []lipgloss.Color{
	lipgloss.Color("#FF6B6B"),
	lipgloss.Color("#FFA86B"),
	lipgloss.Color("#95E1D3"),
	lipgloss.Color("#4ECDC4"),
	lipgloss.Color("#FF6B6B"),
}

// Brand colors inspired by time progression: Dawn -> Day -> Dusk -> Night.
//
// Two groups. The fixed hues below read well on both light and dark
// terminals, or sit on their own colored chip, so they never change. The
// background-dependent set (FgColor, MutedColor, UnreadColor,
// SecondaryColor) is flipped by applyPalette when the resolved theme
// changes. The flip reuses the same light/dark resolution that drives the
// glamour reader (resolveGlamourStyle) rather than a lipgloss OSC 11
// background probe, which we avoid because it can block startup for seconds.
var (
	PrimaryColor    = lipgloss.Color("#FF6B6B") // Warm coral - dawn
	AccentColor     = lipgloss.Color("#95E1D3") // Mint - selection background
	BackgroundColor = lipgloss.Color("#1A1A2E") // Deep night - chip foreground
	SurfaceColor    = lipgloss.Color("#16213E") // Midnight blue - title bar
	TextColor       = lipgloss.Color("#EAEAEA") // Soft white - text on dark chips
	ReadColor       = lipgloss.Color("#64748B") // Slate - read/past
	StarColor       = lipgloss.Color("#F59E0B") // Amber - starred/favorite
	ErrorColor      = lipgloss.Color("#EF4444") // Red
	SuccessColor    = lipgloss.Color("#10B981") // Green
)

// Background-dependent colors. Zero-valued here; applyPalette assigns them
// for a dark or light terminal.
var (
	FgColor        lipgloss.Color // body/modal text rendered on the terminal bg
	MutedColor     lipgloss.Color // secondary text, hints, separators
	UnreadColor    lipgloss.Color // unread markers, highlights, warnings
	SecondaryColor lipgloss.Color // headers and feed titles
)

// Styled components. Assigned by applyPalette so the ones using a
// background-dependent color rebuild when the theme flips.
var (
	LogoStyle           lipgloss.Style
	TitleStyle          lipgloss.Style
	HeaderStyle         lipgloss.Style
	StatusBarStyle      lipgloss.Style
	UnreadItemStyle     lipgloss.Style
	ReadItemStyle       lipgloss.Style
	StarStyle           lipgloss.Style
	SelectedItemStyle   lipgloss.Style
	HelpStyle           lipgloss.Style
	TimeStyle           lipgloss.Style
	ModalTextStyle      lipgloss.Style
	ModalHighlightStyle lipgloss.Style
	ErrorMessageStyle   lipgloss.Style
	SeparatorStyle      lipgloss.Style
	StatusInfoStyle     lipgloss.Style
	StatusSuccessStyle  lipgloss.Style
	StatusWarnStyle     lipgloss.Style
	StatusErrorStyle    lipgloss.Style
	FeedTitleStyle      lipgloss.Style
	EmptyStyle          lipgloss.Style
)

// init seeds the palette with the dark variant; App overrides it once the
// theme is resolved (and again on every live theme change).
func init() { applyPalette(true) }

// applyPalette sets the background-dependent colors for a dark or light
// terminal and rebuilds every style that uses them. The brand and status
// hues are fixed; only text, muted, unread, and secondary flip. Call it from
// the Bubble Tea update loop (single-goroutine) — it reassigns package
// globals, so it must not race with rendering on another goroutine.
func applyPalette(dark bool) {
	if dark {
		FgColor = lipgloss.Color("#EAEAEA")
		MutedColor = lipgloss.Color("#94A3B8")     // Muted gray-blue
		UnreadColor = lipgloss.Color("#FFE66D")    // Bright yellow - new/unread
		SecondaryColor = lipgloss.Color("#4ECDC4") // Teal - morning
	} else {
		FgColor = lipgloss.Color("#1A1A2E")        // Dark ink on a light terminal
		MutedColor = lipgloss.Color("#57636E")     // Slate, darker for white-bg contrast
		UnreadColor = lipgloss.Color("#B45309")    // Amber-700; yellow is unreadable on white
		SecondaryColor = lipgloss.Color("#0E7490") // Cyan-700; teal is too pale on white
	}

	LogoStyle = lipgloss.NewStyle().Foreground(PrimaryColor).Bold(true)
	TitleStyle = lipgloss.NewStyle().Foreground(TextColor).Background(SurfaceColor).Bold(true).Padding(0, 2)
	HeaderStyle = lipgloss.NewStyle().Foreground(SecondaryColor).Bold(true)
	StatusBarStyle = lipgloss.NewStyle().Foreground(MutedColor).Padding(0, 1)
	UnreadItemStyle = lipgloss.NewStyle().Foreground(UnreadColor).Bold(true)
	ReadItemStyle = lipgloss.NewStyle().Foreground(ReadColor)
	StarStyle = lipgloss.NewStyle().Foreground(StarColor).Bold(true)
	SelectedItemStyle = lipgloss.NewStyle().Foreground(BackgroundColor).Background(AccentColor).Bold(true)
	HelpStyle = lipgloss.NewStyle().Foreground(MutedColor).Italic(true)
	TimeStyle = lipgloss.NewStyle().Foreground(MutedColor).Faint(true)
	ModalTextStyle = lipgloss.NewStyle().Foreground(FgColor)
	ModalHighlightStyle = lipgloss.NewStyle().Foreground(UnreadColor).Bold(true)
	ErrorMessageStyle = lipgloss.NewStyle().Foreground(ErrorColor).Bold(true)
	SeparatorStyle = lipgloss.NewStyle().Foreground(MutedColor)
	StatusInfoStyle = lipgloss.NewStyle().Foreground(MutedColor)
	StatusSuccessStyle = lipgloss.NewStyle().Foreground(SuccessColor)
	StatusWarnStyle = lipgloss.NewStyle().Foreground(UnreadColor)
	StatusErrorStyle = lipgloss.NewStyle().Foreground(ErrorColor).Bold(true)
	FeedTitleStyle = lipgloss.NewStyle().Foreground(SecondaryColor).Bold(true)
	EmptyStyle = lipgloss.NewStyle()
}

// StatusBarStyleWithPadding returns a properly formatted status bar style with padding
func StatusBarStyleWithPadding() lipgloss.Style {
	return StatusBarStyle.Padding(0, 1)
}

// ContentWrapper returns a style for wrapping content with width and height constraints
func ContentWrapper(width, height int) lipgloss.Style {
	return EmptyStyle.Width(width).Height(height).MaxHeight(height)
}

func GetWelcomeMessage() string {
	return GetCompactBanner("Press ctrl+n to add your first feed")
}

func GetCompactBanner(message string) string {
	// Use the canonical logo lines
	var coloredLines []string
	for _, line := range LogoLines {
		coloredLines = append(coloredLines, LogoStyle.Render(line))
	}

	logo := lipgloss.JoinVertical(lipgloss.Center, coloredLines...)

	return lipgloss.JoinVertical(
		lipgloss.Center,
		logo,
		"",
		HelpStyle.Render(message),
	)
}

func ShowBanner(version string) {
	// Start with the canonical logo lines and add empty line
	lines := make([]string, len(LogoLines)+1)
	copy(lines, LogoLines)
	lines[len(LogoLines)] = ""

	// Dynamic version tagline
	versionTag := version
	if versionTag != "" && versionTag != "dev" {
		// prefix with 'v' if not already prefixed
		if versionTag[0] != 'v' && versionTag[0] != 'V' {
			versionTag = "v" + versionTag
		}
		lines = append(lines, fmt.Sprintf("    RSS Feed Aggregator %s", versionTag))
	} else {
		lines = append(lines, "    RSS Feed Aggregator")
	}

	// Apply gradient coloring to each line
	var coloredLines []string
	for i, line := range lines {
		if line == "" {
			coloredLines = append(coloredLines, line)
			continue
		}

		// Pick color based on line index
		colorIdx := i % len(BannerColors)
		style := lipgloss.NewStyle().
			Foreground(BannerColors[colorIdx]).
			Bold(i < len(LogoLines)) // Bold for logo, normal for tagline

		coloredLines = append(coloredLines, style.Render(line))
	}

	// Create fancy border with animations-like characters
	borderChars := lipgloss.Border{
		Top:         "═",
		Bottom:      "═",
		Left:        "║",
		Right:       "║",
		TopLeft:     "╔",
		TopRight:    "╗",
		BottomLeft:  "╚",
		BottomRight: "╝",
	}

	borderStyle := lipgloss.NewStyle().
		Border(borderChars).
		BorderForeground(lipgloss.Color("#4ECDC4")).
		Padding(1, 3).
		MarginTop(1)

	// Join all lines and render with border
	banner := lipgloss.JoinVertical(lipgloss.Center, coloredLines...)
	output := borderStyle.Render(banner)

	// Center the entire banner
	fmt.Println(lipgloss.NewStyle().
		Width(70).
		Align(lipgloss.Center).
		Render(output))

	// Add a subtle separator line below
	separator := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#95E1D3")).
		Render("◆ ◇ ◆ ◇ ◆")

	fmt.Println(lipgloss.NewStyle().
		Width(70).
		Align(lipgloss.Center).
		MarginBottom(1).
		Render(separator))
}
