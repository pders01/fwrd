package tui

import (
	"html"
	"regexp"
	"strings"
	"sync"
	"unicode"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/microcosm-cc/bluemonday"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/terminaltext"
)

// htmlTagRe matches an opening, self-closing, or closing HTML tag whose
// name starts with an ASCII letter. The letter-start requirement avoids
// false positives on prose like "if x < 3 then y > 2" or markdown that
// happens to contain stray angle brackets.
var htmlTagRe = regexp.MustCompile(`(?i)<\s*/?\s*[a-z][a-z0-9]{0,15}(\s[^<>]*)?/?\s*>`)

var numericEntityRe = regexp.MustCompile(`&#(?:[xX][0-9a-fA-F]+|\d+);?`)

// Glamour decodes Markdown character references while styling. Materialize
// only control-valued references before Safe, so entities cannot resurrect
// escapes AFTER sanitization. Do not decode ordinary entities here: doing so
// could turn &amp;#27; into a new active reference on Glamour's second pass.
func safeMarkdownInput(s string) string {
	s = terminaltext.Safe(s)
	s = numericEntityRe.ReplaceAllStringFunc(s, func(entity string) string {
		decoded := html.UnescapeString(entity)
		if strings.ContainsFunc(decoded, unicode.IsControl) {
			return decoded
		}
		return entity
	})
	s = terminaltext.Safe(s)
	// Removing a control string can concatenate text into a NEW reference
	// (e.g. "&&#11;#11"). Neutralize these with a space, not another decode
	// and strip cycle, to prevent both reassembly and quadratic rescanning.
	return numericEntityRe.ReplaceAllStringFunc(s, func(entity string) string {
		if strings.ContainsFunc(html.UnescapeString(entity), unicode.IsControl) {
			return " "
		}
		return entity
	})
}

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
	s = safeMarkdownInput(s)
	if !looksLikeHTML(s) {
		return s
	}
	clean := getSanitizer().Sanitize(s)
	md, err := htmltomarkdown.ConvertString(clean)
	if err != nil {
		debuglog.Warnf("html-to-markdown convert failed: %v", err)
		return safeMarkdownInput(clean)
	}
	return safeMarkdownInput(md)
}
