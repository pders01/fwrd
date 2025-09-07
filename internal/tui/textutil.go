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

// truncateMiddle shortens s to at most limit characters by preserving the
// start and end of the string with a single ellipsis in the middle.
// Useful for URLs and paths where both ends carry meaning.
func truncateMiddle(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	n := len(r)
	if n <= limit {
		return s
	}
	if limit <= 1 {
		return "…"
	}
	// Split remaining space equally around the ellipsis
	keep := limit - 1
	left := keep / 2
	right := keep - left
	if left <= 0 {
		return "…" + string(r[n-right:])
	}
	if right <= 0 {
		return string(r[:left]) + "…"
	}
	return string(r[:left]) + "…" + string(r[n-right:])
}
