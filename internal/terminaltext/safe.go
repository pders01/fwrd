// Package terminaltext sanitizes untrusted text before terminal styling.
package terminaltext

import (
	"strings"
	"unicode/utf8"
)

// Safe removes terminal escape sequences and C0/C1 controls, except newline
// and tab. It accepts both UTF-8 and legacy single-byte C1 encodings. Apply it
// to untrusted input BEFORE adding application ANSI styles, never to a rendered
// screen. Unterminated control strings are discarded through end of input.
func Safe(s string) string {
	const (
		plain = iota
		escape
		intermediate
		csi
		controlString
		stringEscape
	)
	state := plain
	var out strings.Builder
	out.Grow(len(s))
	for s != "" {
		r, n := utf8.DecodeRuneInString(s)
		if r == utf8.RuneError && n == 1 && s[0] >= 0x80 && s[0] <= 0x9f {
			r = rune(s[0])
		}
		s = s[n:]
		if state != plain && (r == 0x18 || r == 0x1a) { // CAN / SUB
			state = plain
			continue
		}
		switch state {
		case controlString, stringEscape:
			switch {
			case r == 0x07 || r == 0x9c || (state == stringEscape && r == '\\'):
				state = plain
			case r == 0x1b:
				state = stringEscape
			default:
				state = controlString
			}
			continue
		case csi:
			if r == 0x1b {
				state = escape
			} else if r >= 0x40 && r <= 0x7e {
				state = plain
			}
			continue
		case escape:
			switch r {
			case '[':
				state = csi
			case ']', 'P', 'X', '^', '_':
				state = controlString
			case 0x1b:
				state = escape
			default:
				if r >= 0x20 && r <= 0x2f {
					state = intermediate
				} else {
					state = plain
				}
			}
			continue
		case intermediate:
			if r == 0x1b {
				state = escape
			} else if r < 0x20 || r > 0x2f {
				state = plain
			}
			continue
		}
		switch r {
		case 0x1b:
			state = escape
		case 0x9b:
			state = csi
		case 0x90, 0x98, 0x9d, 0x9e, 0x9f:
			state = controlString
		default:
			if r == '\n' || r == '\t' || (r >= 0x20 && (r < 0x7f || r > 0x9f)) {
				out.WriteRune(r)
			}
		}
	}
	return out.String()
}
