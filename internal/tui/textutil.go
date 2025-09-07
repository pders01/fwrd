package tui

// truncateEnd shortens s to at most max characters, appending an ellipsis
// if truncation occurs. Handles negative or tiny limits gracefully.
func truncateEnd(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	// Convert to runes for safety
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	if limit <= 1 {
		return "…"
	}
	return string(r[:limit-1]) + "…"
}
