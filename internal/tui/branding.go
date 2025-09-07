package tui

import (
	"github.com/charmbracelet/lipgloss"
)

const AppName = "fwrd"

// ASCII art logo for fwrd
const Logo = `
  ___                     _ 
 / _|_      ___ __ __| |
| |_\ \ /\ / / '__/ _' |
|  _|\ V  V /| | | (_| |
|_|   \_/\_/ |_|  \__,_|
`

const CompactLogo = `fwrd ›`

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
			Background(SurfaceColor).
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
)

func GetWelcomeMessage() string {
	return lipgloss.JoinVertical(
		lipgloss.Center,
		LogoStyle.Render(Logo),
		"",
		HelpStyle.Render("Press 'a' to add your first feed"),
	)
}
