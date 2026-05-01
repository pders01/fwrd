package tui

import (
	"regexp"
	"sync"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/microcosm-cc/bluemonday"
	"github.com/pders01/fwrd/internal/debuglog"
)

// htmlTagRe matches an opening, self-closing, or closing HTML tag whose
// name starts with an ASCII letter. The letter-start requirement avoids
// false positives on prose like "if x < 3 then y > 2" or markdown that
// happens to contain stray angle brackets.
var htmlTagRe = regexp.MustCompile(`(?i)<\s*/?\s*[a-z][a-z0-9]{0,15}(\s[^<>]*)?/?\s*>`)

func looksLikeHTML(s string) bool {
	return htmlTagRe.MatchString(s)
}

// sanitizerPolicy is built once: bluemonday's UGCPolicy strips <script>,
// <style>, <iframe>, all event handlers (on*), and rejects javascript:
// and data: URLs in href/src by default. Treat every feed body as
// untrusted user-generated content.
var (
	sanitizerOnce sync.Once
	sanitizer     *bluemonday.Policy
)

func getSanitizer() *bluemonday.Policy {
	sanitizerOnce.Do(func() {
		sanitizer = bluemonday.UGCPolicy()
	})
	return sanitizer
}

// htmlToMarkdown sanitizes HTML feed content and converts it to Markdown
// for glamour rendering. Input that does not look like HTML is returned
// unchanged. All HTML is treated as dangerous: even though terminal
// rendering won't execute scripts, malicious markup can still smuggle
// tracker pixels, javascript: URLs, or unbounded inline styles into the
// output. We sanitize before conversion so the converter only ever sees
// safe HTML, and so removed elements don't leak into the markdown.
func htmlToMarkdown(s string) string {
	if !looksLikeHTML(s) {
		return s
	}
	clean := getSanitizer().Sanitize(s)
	md, err := htmltomarkdown.ConvertString(clean)
	if err != nil {
		debuglog.Warnf("html-to-markdown convert failed: %v", err)
		return clean
	}
	return md
}
