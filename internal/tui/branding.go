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

// Brand colors inspired by time progression
// Dawn -> Day -> Dusk -> Night
var (
	// Primary colors - gradient from dawn to day
	PrimaryColor   = lipgloss.Color("#FF6B6B") // Warm coral - dawn
	SecondaryColor = lipgloss.Color("#4ECDC4") // Teal - morning
	AccentColor    = lipgloss.Color("#95E1D3") // Mint - fresh start

	// UI colors
	BackgroundColor = lipgloss.Color("#1A1A2E") // Deep night
	SurfaceColor    = lipgloss.Color("#16213E") // Midnight blue
	TextColor       = lipgloss.Color("#EAEAEA") // Soft white
	MutedColor      = lipgloss.Color("#94A3B8") // Muted gray-blue

	// Status colors
	UnreadColor  = lipgloss.Color("#FFE66D") // Bright yellow - new/unread
	ReadColor    = lipgloss.Color("#64748B") // Slate - read/past
	ErrorColor   = lipgloss.Color("#EF4444") // Red
	SuccessColor = lipgloss.Color("#10B981") // Green
)

// Styled components
var (
	LogoStyle = lipgloss.NewStyle().
			Foreground(PrimaryColor).
			Bold(true)

	TitleStyle = lipgloss.NewStyle().
			Foreground(TextColor).
			Background(SurfaceColor).
			Bold(true).
			Padding(0, 2)

	HeaderStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor).
			Bold(true)

	StatusBarStyle = lipgloss.NewStyle().
			Foreground(MutedColor).
			Padding(0, 1)

	UnreadItemStyle = lipgloss.NewStyle().
			Foreground(UnreadColor).
			Bold(true)

	ReadItemStyle = lipgloss.NewStyle().
			Foreground(ReadColor)

	SelectedItemStyle = lipgloss.NewStyle().
				Foreground(BackgroundColor).
				Background(AccentColor).
				Bold(true)

	HelpStyle = lipgloss.NewStyle().
			Foreground(MutedColor).
			Italic(true)

	TimeStyle = lipgloss.NewStyle().
			Foreground(MutedColor).
			Faint(true)

	// Modal styles
	ModalTextStyle = lipgloss.NewStyle().
			Foreground(TextColor)

	ModalHighlightStyle = lipgloss.NewStyle().
				Foreground(UnreadColor).
				Bold(true)

	// Error display style
	ErrorMessageStyle = lipgloss.NewStyle().
				Foreground(ErrorColor).
				Bold(true)

	// Separator style
	SeparatorStyle = lipgloss.NewStyle().
			Foreground(MutedColor)

	// Status styles by severity
	StatusInfoStyle = lipgloss.NewStyle().
			Foreground(MutedColor)

	StatusSuccessStyle = lipgloss.NewStyle().
				Foreground(SuccessColor)

	StatusWarnStyle = lipgloss.NewStyle().
			Foreground(UnreadColor)

	StatusErrorStyle = lipgloss.NewStyle().
				Foreground(ErrorColor).
				Bold(true)

	// Feed item styles
	FeedTitleStyle = lipgloss.NewStyle().
			Foreground(SecondaryColor).
			Bold(true)

	// Empty style for resetting
	EmptyStyle = lipgloss.NewStyle()
)

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
