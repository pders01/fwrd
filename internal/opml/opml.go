// Package opml reads and writes OPML 2.0 subscription lists, the common
// interchange format for moving feed subscriptions between aggregators.
// It is shared by the CLI (fwrd feed import/export) and the web view
// (/opml/import, /opml/export) so both speak the same dialect.
package opml

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

// maxOPMLSize bounds the bytes Parse will read from any source. Even a
// large subscription list is a few hundred KiB; this is a generous ceiling
// that still rejects a pathological document.
const maxOPMLSize = 8 << 20 // 8 MiB

// document is the root OPML element. Only the subset fwrd produces and
// consumes is modelled; unknown elements and attributes are ignored on
// parse, which is what lets us read OPML written by other readers.
type document struct {
	XMLName xml.Name `xml:"opml"`
	Version string   `xml:"version,attr"`
	Head    head     `xml:"head"`
	Body    body     `xml:"body"`
}

type head struct {
	Title       string `xml:"title,omitempty"`
	DateCreated string `xml:"dateCreated,omitempty"`
}

type body struct {
	Outlines []outline `xml:"outline"`
}

// outline is one node. Feed outlines carry an xmlUrl; container outlines
// (categories) carry nested children instead. Both shapes appear in real
// OPML, so import walks the tree and collects every node with an xmlUrl.
type outline struct {
	Text     string    `xml:"text,attr,omitempty"`
	Title    string    `xml:"title,attr,omitempty"`
	Type     string    `xml:"type,attr,omitempty"`
	XMLURL   string    `xml:"xmlUrl,attr,omitempty"`
	HTMLURL  string    `xml:"htmlUrl,attr,omitempty"`
	Children []outline `xml:"outline"`
}

// Feed is a single subscription recovered from an OPML document: the feed
// URL and a human-readable title. It is intentionally smaller than
// storage.Feed — import only knows the URL and title; the rest is filled
// in when the feed is actually fetched.
type Feed struct {
	URL   string
	Title string
}

// Export renders feeds as an OPML 2.0 document. created stamps the head's
// dateCreated (RFC 1123); pass the zero time to omit it. Feeds without a
// URL are skipped — an outline with no xmlUrl is not a subscription.
func Export(feeds []*storage.Feed, created time.Time) ([]byte, error) {
	doc := document{
		Version: "2.0",
		Head:    head{Title: "fwrd subscriptions"},
	}
	if !created.IsZero() {
		doc.Head.DateCreated = created.Format(time.RFC1123Z)
	}
	for _, f := range feeds {
		if f == nil || strings.TrimSpace(f.URL) == "" {
			continue
		}
		title := f.Title
		if title == "" {
			title = f.URL
		}
		doc.Body.Outlines = append(doc.Body.Outlines, outline{
			Text:   title,
			Title:  title,
			Type:   "rss",
			XMLURL: f.URL,
		})
	}

	out, err := xml.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal opml: %w", err)
	}
	// MarshalIndent omits the XML declaration; prepend it so the output is
	// a well-formed standalone document other readers will accept.
	return append([]byte(xml.Header), append(out, '\n')...), nil
}

// Parse reads an OPML document and returns the feeds it lists. The outline
// tree is walked depth-first so feeds nested under category outlines are
// recovered too. Duplicate xmlUrls are collapsed, keeping the first title
// seen. A document with no feed outlines parses cleanly to an empty slice.
func Parse(r io.Reader) ([]Feed, error) {
	var doc document
	// Bound total input so a pathological document can't exhaust memory,
	// regardless of caller. Go's encoding/xml does not expand custom DTD
	// entities (an undefined &entity; is a parse error), so there is no
	// billion-laughs vector to defend against — this only caps raw size.
	// Subscription lists are small; maxOPMLSize is a generous ceiling.
	dec := xml.NewDecoder(io.LimitReader(r, maxOPMLSize))
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse opml: %w", err)
	}

	var feeds []Feed
	seen := make(map[string]bool)
	var walk func(outlines []outline)
	walk = func(outlines []outline) {
		for _, o := range outlines {
			url := strings.TrimSpace(o.XMLURL)
			if url != "" && !seen[url] {
				seen[url] = true
				title := o.Title
				if title == "" {
					title = o.Text
				}
				feeds = append(feeds, Feed{URL: url, Title: strings.TrimSpace(title)})
			}
			if len(o.Children) > 0 {
				walk(o.Children)
			}
		}
	}
	walk(doc.Body.Outlines)
	return feeds, nil
}
