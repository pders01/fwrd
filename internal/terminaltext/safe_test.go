package terminaltext

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestSafe(t *testing.T) {
	for _, tt := range []struct{ name, input, want string }{
		{"prose", "日本語 café\n\tİstanbul", "日本語 café\n\tİstanbul"},
		{"CSI", "before\x1b[2J\x1b[31mafter\x1b[0m", "beforeafter"},
		{"OSC clipboard", "a\x1b]52;c;c2VjcmV0\ab", "ab"},
		{"OSC hyperlink", "\x1b]8;;https://evil.test\x1b\\label\x1b]8;;\x1b\\", "label"},
		{"DCS", "a\x1bPpayload\x1b\\b", "ab"},
		{"other strings", "a\x1bXsecret\x1b\\b\x1b^secret\x1b\\c\x1b_secret\x1b\\d", "abcd"},
		{"UTF8 C1", "a\u009b2Jb\u009d52;c;data\u009cc", "abc"},
		{"raw C1", "a\x9b2Jb\x9d52;c;data\x9cc", "abc"},
		{"C0 C1", "a\x00\x01\x07\x08\x0b\x0c\r\x7f\u0085b", "ab"},
		{"charset", "a\x1b(Bb\x1b7c", "abc"},
		{"unterminated OSC", "safe\x1b]52;c;secret", "safe"},
		{"unterminated CSI", "safe\x1b[123;", "safe"},
		{"cancel", "a\x1b[123\x18b", "ab"},
		{"invalid UTF8", "a\xffb", "a�b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := Safe(tt.input); got != tt.want {
				t.Fatalf("Safe(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func FuzzSafe(f *testing.F) {
	for _, seed := range []string{"hello", "\x1b]52;c;x\a", "\u009b2J", "\x9d8;;evil\x9c", "\xff\x1b[", "日本語\n\t"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		out := Safe(input)
		if !utf8.ValidString(out) || Safe(out) != out {
			t.Fatalf("invalid or non-idempotent output: %q", out)
		}
		for _, r := range out {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				t.Fatalf("control %U survives in %q", r, out)
			}
		}
	})
}
