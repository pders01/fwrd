package tui

import (
	"strings"
	"testing"
)

func TestLooksLikeHTML(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"empty", "", false},
		{"plain prose", "Hello world, this is a sentence.", false},
		{"markdown only", "**bold** and [link](https://example.com)", false},
		{"math comparison", "if x < 3 then y > 2 and z < 10", false},
		{"escaped entities", "use &lt;p&gt; for paragraphs", false},
		{"stray angle digits", "version 1<2 and 2>1", false},
		{"single paragraph", "<p>hello</p>", true},
		{"self-closing br", "line one<br/>line two", true},
		{"self-closing space", "line one<br />line two", true},
		{"link tag", `text <a href="x">y</a> more`, true},
		{"img tag", `<img src="x.png" alt="x">`, true},
		{"closing tag only", "trailing </div>", true},
		{"uppercase", "<P>hi</P>", true},
		{"mixed plain + tag", "preamble <strong>x</strong> tail", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeHTML(tc.in); got != tc.want {
				t.Errorf("looksLikeHTML(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestHTMLToMarkdown_PassthroughForNonHTML(t *testing.T) {
	in := "Plain text with **markdown** and no tags."
	if got := htmlToMarkdown(in); got != in {
		t.Errorf("non-HTML input was modified.\n got: %q\nwant: %q", got, in)
	}
}

func TestHTMLToMarkdown_ConvertsBasicHTML(t *testing.T) {
	in := `<h1>Title</h1><p>Hello <strong>world</strong>.</p><p>Visit <a href="https://example.com">example</a>.</p>`
	got := htmlToMarkdown(in)
	for _, want := range []string{"# Title", "**world**", "[example](https://example.com)"} {
		if !strings.Contains(got, want) {
			t.Errorf("converted markdown missing %q\nfull output:\n%s", want, got)
		}
	}
	if strings.Contains(got, "<p>") || strings.Contains(got, "<strong>") {
		t.Errorf("converted markdown still contains raw HTML tags:\n%s", got)
	}
}

func TestHTMLToMarkdown_StripsDangerousMarkup(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		mustNot  []string
		mustKeep []string // optional substrings that should survive
	}{
		{
			name:    "script tag and body",
			in:      `<p>safe</p><script>alert('xss')</script><p>more</p>`,
			mustNot: []string{"alert", "<script", "script>"},
		},
		{
			name:     "inline event handler",
			in:       `<a href="https://example.com" onclick="steal()">click</a>`,
			mustNot:  []string{"onclick", "steal"},
			mustKeep: []string{"https://example.com"},
		},
		{
			name:    "javascript: href",
			in:      `<a href="javascript:alert(1)">tap</a>`,
			mustNot: []string{"javascript:", "alert"},
		},
		{
			name:    "iframe",
			in:      `<p>before</p><iframe src="https://evil.example.com"></iframe><p>after</p>`,
			mustNot: []string{"<iframe", "evil.example.com"},
		},
		{
			name:     "style block",
			in:       `<style>body{display:none}</style><p>visible</p>`,
			mustNot:  []string{"display:none", "<style"},
			mustKeep: []string{"visible"},
		},
		{
			name:    "data: URL image",
			in:      `<img src="data:text/html;base64,PHNjcmlwdD4=" alt="x">`,
			mustNot: []string{"data:text/html", "PHNjcmlwdD4"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := htmlToMarkdown(tc.in)
			for _, bad := range tc.mustNot {
				if strings.Contains(got, bad) {
					t.Errorf("output contains forbidden substring %q\noutput: %q", bad, got)
				}
			}
			for _, ok := range tc.mustKeep {
				if !strings.Contains(got, ok) {
					t.Errorf("output missing expected substring %q\noutput: %q", ok, got)
				}
			}
		})
	}
}
