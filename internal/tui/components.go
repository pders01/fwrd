package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// renderHeader returns a consistently styled header with an optional muted subtitle.
func renderHeader(title, subtitle string) string {
	rows := []string{HeaderStyle.Render(title)}
	if subtitle != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(MutedColor).Render(subtitle))
	}
	return lipgloss.JoinVertical(lipgloss.Top, rows...)
}

// renderInputFrame draws a rounded bordered container around a rendered input view.
// Pass the already-rendered input view string.
//
//revive:disable-next-line:unused
func renderInputFrame(inputView string, focused bool, contentWidth int) string {
	borderColor := MutedColor
	if focused {
		borderColor = AccentColor
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(contentWidth + 4).
		Render(inputView)
}

// renderCentered centers the provided content within the given width/height box.
//
//revive:disable-next-line:unused
func renderCentered(width, height int, content string) string {
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(content)
}
