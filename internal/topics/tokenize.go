package topics

import (
	"strings"
	"unicode"
)

// tokenize splits text into a bag of significant word counts: lowercased
// alphabetic runs of length >= 3 that are not stopwords and not purely
// numeric. The returned map is term -> count within this one document.
func tokenize(text string) map[string]int {
	counts := map[string]int{}
	var b strings.Builder
	flush := func() {
		if b.Len() == 0 {
			return
		}
		w := b.String()
		b.Reset()
		if len(w) < 3 || stopwords[w] {
			return
		}
		counts[w]++
	}
	for _, r := range strings.ToLower(text) {
		if unicode.IsLetter(r) {
			b.WriteRune(r)
			continue
		}
		// Keep digits inside a token (e.g. "gpt4", "ipv6") but a token that
		// is all digits is dropped by the letter check below.
		if unicode.IsDigit(r) && b.Len() > 0 {
			b.WriteRune(r)
			continue
		}
		flush()
	}
	flush()

	// Drop tokens with no letters at all (pure digit runs that slipped in).
	for w := range counts {
		if !hasLetter(w) {
			delete(counts, w)
		}
	}
	return counts
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

// plain strips HTML tags and decodes a handful of common entities so feed
// descriptions tokenize as prose rather than markup. It is intentionally
// lightweight — exact HTML parsing is unnecessary for a bag-of-words.
func plain(s string) string {
	if s == "" {
		return ""
	}
	s = stripURLs(s)
	var b strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			b.WriteByte(' ')
		case !inTag:
			b.WriteRune(r)
		}
	}
	out := b.String()
	for _, e := range []struct{ from, to string }{
		{"&amp;", " "}, {"&lt;", " "}, {"&gt;", " "}, {"&quot;", " "},
		{"&#39;", " "}, {"&nbsp;", " "}, {"&mdash;", " "}, {"&ndash;", " "},
	} {
		out = strings.ReplaceAll(out, e.from, e.to)
	}
	return out
}

// stripURLs removes http(s):// and www. URL runs so link text does not
// tokenize into domain words ("tildes", "ycombinator", "github") that
// would otherwise cluster as bogus topics. A URL run ends at whitespace or
// an angle bracket.
func stripURLs(s string) string {
	lower := strings.ToLower(s)
	var b strings.Builder
	for i := 0; i < len(s); {
		if strings.HasPrefix(lower[i:], "http://") ||
			strings.HasPrefix(lower[i:], "https://") ||
			strings.HasPrefix(lower[i:], "www.") {
			j := i
			for j < len(s) && !isURLBoundary(rune(s[j])) {
				j++
			}
			b.WriteByte(' ')
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isURLBoundary(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '<' || r == '>' || r == '"' || r == '\''
}

// stopwords is a compact English + web/blog boilerplate list. It does not
// aim to be exhaustive; TF-IDF already suppresses corpus-wide common terms.
// This trims the highest-frequency function words that would otherwise eat
// a per-document signature slot.
var stopwords = func() map[string]bool {
	words := strings.Fields(`
the and for are but not you all any can had her was one our out day get has him his how man new now old see two way who boy did its let put say she too use
this that with have from they will would there their what about which when make like time just know take people into year your good some could them than then
been being over also after most other such only very even back even where these those while should each both being does done upon
http https www com org net html htm rss feed blog post posts read more comment comments share tweet email subscribe newsletter via using used use uses
article articles page pages site website link links click here today week month
url uri utc gmt est pst edt pdt cet cest jst time date update updates updated new news
january february march april may june july august september october november december
mon tue wed thu fri sat sun jan feb mar apr jun jul aug sep sept oct nov dec
points score scored comments scheduled maintenance posted published author
completed resolved investigating monitoring identified degraded operational incident outage status
`)
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}()
