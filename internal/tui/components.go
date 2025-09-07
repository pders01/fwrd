package tui

import (
    "github.com/charmbracelet/lipgloss"
)

// renderHeader returns a consistently styled header with an optional muted subtitle.
// Width is used to guide truncation via helpers.
func renderHeader(title, subtitle string, width int) string {
    title = truncateEnd(title, width-2)
    subtitle = truncateEnd(subtitle, width-2)
    rows := []string{HeaderStyle.Render(title)}
    if subtitle != "" {
        rows = append(rows, renderMuted(subtitle))
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

// renderMuted renders text in muted color (utility wrapper).
func renderMuted(text string) string {
    return lipgloss.NewStyle().Foreground(MutedColor).Render(text)
}

// renderHelp renders help/instructional text consistently.
func renderHelp(text string) string {
    return HelpStyle.Render(text)
}
