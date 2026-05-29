package web

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/topics"
)

// frontCorpus bounds how many recent articles (newest-first, across all
// feeds) the front page and topic pages cluster over. A few hundred keeps
// clustering fast while covering everything a reader is likely to scan.
const frontCorpus = 400

// headlinesPerSection caps how many articles a front-page section block
// shows before linking to its full topic page.
const headlinesPerSection = 6

// deckLen bounds the lead story's plain-text deck.
const deckLen = 220

type headlineView struct {
	Article *storage.Article
	Feed    string
}

type sectionView struct {
	Slug      string
	Label     string
	Headlines []headlineView
	More      int // articles beyond those shown, for the "+N more" link
}

type frontData struct {
	Now        time.Time
	HasContent bool
	Lead       *storage.Article
	LeadFeed   string
	LeadDeck   string
	Sections   []sectionView
}

type topicData struct {
	Label    string
	Slug     string
	Articles []headlineView
}

// topicOptions returns the clustering options for the web view, stamped
// with the current time so future-dated and undated articles are ranked
// stale rather than leading the page.
func topicOptions() topics.Options {
	o := topics.DefaultOptions()
	o.Now = time.Now()
	return o
}

// handleFront renders the newspaper front page: a masthead, one lead story
// (the most recent article across all feeds), and topical section blocks
// derived from the corpus. Topics are recomputed per request — the corpus
// is bounded and clustering is cheap — so read/star state is always fresh.
func (s *Server) handleFront(w http.ResponseWriter, _ *http.Request) {
	arts, err := s.store.GetArticles("", frontCorpus)
	if err != nil {
		http.Error(w, "failed to load articles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	names := s.feedNames()
	model := topics.Build(arts, topicOptions())

	data := frontData{Now: time.Now(), HasContent: model.Lead != nil}
	if model.Lead != nil {
		data.Lead = model.Lead
		data.LeadFeed = names[model.Lead.FeedID]
		data.LeadDeck = excerpt(articleBody(model.Lead), deckLen)
	}

	for _, t := range model.Topics {
		sv := sectionView{Slug: t.Slug, Label: t.Label}
		for _, a := range t.Articles {
			// The lead is shown prominently above; don't repeat it in a
			// section block (it still belongs to the topic page).
			if model.Lead != nil && a.ID == model.Lead.ID {
				continue
			}
			if len(sv.Headlines) < headlinesPerSection {
				sv.Headlines = append(sv.Headlines, headlineView{Article: a, Feed: names[a.FeedID]})
			}
		}
		shown := len(sv.Headlines)
		total := len(t.Articles)
		if model.Lead != nil && containsArticle(t.Articles, model.Lead.ID) {
			total--
		}
		sv.More = total - shown
		if shown > 0 {
			data.Sections = append(data.Sections, sv)
		}
	}
	s.render(w, "front.html", data)
}

// handleTopic renders one topical section's full article list. The corpus
// and clustering match the front page, so a slug linked from the front
// page resolves to the same topic here.
func (s *Server) handleTopic(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	arts, err := s.store.GetArticles("", frontCorpus)
	if err != nil {
		http.Error(w, "failed to load articles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	model := topics.Build(arts, topicOptions())
	t := model.BySlug(slug)
	if t == nil {
		http.NotFound(w, r)
		return
	}
	names := s.feedNames()
	data := topicData{Label: t.Label, Slug: t.Slug}
	for _, a := range t.Articles {
		data.Articles = append(data.Articles, headlineView{Article: a, Feed: names[a.FeedID]})
	}
	s.render(w, "topic.html", data)
}

// feedNames maps feed ID to a display label (title, or host of the URL
// when untitled), for article bylines.
func (s *Server) feedNames() map[string]string {
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		return map[string]string{}
	}
	m := make(map[string]string, len(feeds))
	for _, f := range feeds {
		m[f.ID] = feedLabel(f)
	}
	return m
}

func feedLabel(f *storage.Feed) string {
	if f.Title != "" {
		return f.Title
	}
	if h := feedHost(f); h != "" {
		return h
	}
	return f.URL
}

// feedHost is the feed URL's host with a leading "www." trimmed, or "" if
// the URL has no parseable host. It is the bare-host fallback label.
func feedHost(f *storage.Feed) string {
	if u, err := url.Parse(f.URL); err == nil && u.Host != "" {
		return strings.TrimPrefix(u.Host, "www.")
	}
	return ""
}

// feedSource is host+path (scheme, "www.", and a trailing slash stripped),
// used as a disambiguating subtitle on the feed-management page. Three
// "arXiv" feeds sharing a title and host are still distinguishable by path
// (arxiv.org/rss/cs.AI vs cs.LG). Empty when the URL has no parseable host.
func feedSource(f *storage.Feed) string {
	u, err := url.Parse(f.URL)
	if err != nil || u.Host == "" {
		return ""
	}
	s := strings.TrimPrefix(u.Host, "www.") + u.Path
	return strings.TrimSuffix(s, "/")
}

func containsArticle(arts []*storage.Article, id string) bool {
	for _, a := range arts {
		if a.ID == id {
			return true
		}
	}
	return false
}

// excerpt strips HTML tags from s and truncates the result to about n
// runes on a word boundary, appending an ellipsis when shortened. Used for
// the lead story's deck.
func excerpt(s string, n int) string {
	plain := stripTags(s)
	plain = strings.Join(strings.Fields(plain), " ") // collapse whitespace
	if len(plain) <= n {
		return plain
	}
	cut := plain[:n]
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

// stripTags removes HTML tags without a full parse — adequate for deriving
// plain text from already-sanitized feed content.
func stripTags(s string) string {
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
	return b.String()
}
