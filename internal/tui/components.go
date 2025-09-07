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
	frameWidth := min(max(contentWidth, 40), 80)
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(frameWidth).
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

// renderModalText renders text centered within a modal of specified width using the given style.
func renderModalText(style *lipgloss.Style, text string, width int) string {
	return style.Width(width).Align(lipgloss.Center).Render(text)
}

// renderModalQuestion renders a centered question text using ModalTextStyle.
func renderModalQuestion(text string, width int) string {
	return renderModalText(&ModalTextStyle, text, width)
}

// renderModalHighlight renders a centered highlighted text using ModalHighlightStyle.
func renderModalHighlight(text string, width int) string {
	return renderModalText(&ModalHighlightStyle, text, width)
}

// renderModalInfo renders a centered informational text using EmptyStyle.
func renderModalInfo(text string, width int) string {
	return renderModalText(&EmptyStyle, text, width)
}

// Width calculation helpers to reduce magic numbers and promote consistency

// getContentWidth calculates width for content, accounting for typical margins/padding.
func getContentWidth(totalWidth int) int {
	return totalWidth - 4 // Standard content margin
}

// getInputWidth calculates width for input fields, accounting for borders and padding.
func getInputWidth(totalWidth int) int {
	inputWidth := totalWidth - 8 // Account for border, padding, and margins
	if inputWidth < 10 {
		inputWidth = totalWidth - 4 // Fallback for narrow screens
	}
	return inputWidth
}

// getModalWidth calculates appropriate width for modal dialogs.
func getModalWidth(totalWidth int) int {
	return totalWidth - 4 // Standard modal margin
}

// getSeparatorWidth calculates width for separator lines.
func getSeparatorWidth(totalWidth int) int {
	return totalWidth - 2 // Account for minimal padding
}

// Truncation helpers for consistent text handling

// truncateForSubtitle truncates text to fit in subtitle space.
func truncateForSubtitle(text string, totalWidth int) string {
	return truncateEnd(text, totalWidth-10) // Standard subtitle margin
}

// truncateForModal truncates text to fit in modal with padding.
func truncateForModal(text string, modalWidth int) string {
	return truncateEnd(text, modalWidth-4) // Modal content padding
}
