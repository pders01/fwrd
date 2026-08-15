package web

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/pders01/fwrd/internal/editorial"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/topics"
)

// frontCorpus bounds the compatibility /topic model. The stable-desk front
// page uses the wider candidate window below, then selects at most 400.
const frontCorpus = 400

// frontCandidates is intentionally larger than the final edition: editorial
// diversity caps must be able to skip a prolific source and keep scanning.
const frontCandidates = 2000

// headlinesPerSection caps how many articles a front-page desk block shows
// before linking to its full desk page.
const headlinesPerSection = 6

// deckLen bounds the lead story's plain-text deck. sectionDeckLen keeps the
// one expanded story in each topic compact enough to preserve scanability.
const (
	deckLen        = 220
	sectionDeckLen = 150
)

type headlineView struct {
	Article *storage.Article
	Feed    string
	Deck    string
}

type topicLink struct {
	Slug  string
	Label string
}

type sectionView struct {
	Slug      string
	Label     string
	Topics    []topicLink
	Spotlight *headlineView
	Headlines []headlineView
	Total     int
	Unread    int
	More      int // articles beyond those shown, for the "+N more" link
}

type frontData struct {
	pageBase
	Now          time.Time
	HasContent   bool
	Lead         *storage.Article
	LeadFeed     string
	LeadDeck     string
	LeadDesk     string
	LeadDeskSlug string
	LeadTopic    string
	ArticleCount int
	UnreadCount  int
	SourceCount  int
	Sections     []sectionView
}

type topicData struct {
	pageBase
	Label    string
	Slug     string
	Articles []headlineView
}

type deskData struct {
	pageBase
	Desk     editorial.Desk
	Topics   []topicLink
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

func editorialOptions() editorial.Options {
	o := editorial.DefaultOptions()
	o.Now = time.Now()
	return o
}

// editorialView returns the stable-desk edition, cached against WriteGen just
// like the compatibility topic model. It loads a wider candidate window so
// composition can apply source caps before selecting the final 400 stories.
func (s *Server) editorialView() (edition *editorial.Edition, names map[string]string) {
	gen := s.store.WriteGen()

	s.frontMu.Lock()
	defer s.frontMu.Unlock()
	if s.editionValid && s.editionGen == gen {
		return s.editionModel, s.editionNames
	}
	articles, err := s.store.GetArticles("", frontCandidates)
	if err != nil {
		if s.editionValid {
			return s.editionModel, s.editionNames
		}
		return editorial.Build(nil, nil, editorialOptions()), map[string]string{}
	}
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		if s.editionValid {
			return s.editionModel, s.editionNames
		}
		return editorial.Build(nil, nil, editorialOptions()), map[string]string{}
	}
	names = make(map[string]string, len(feeds))
	for _, feed := range feeds {
		names[feed.ID] = feedLabel(feed)
	}
	edition = editorial.Build(articles, feeds, editorialOptions())
	s.editionModel, s.editionNames, s.editionGen, s.editionValid = edition, names, gen, true
	return edition, names
}

// frontView returns the front-page topic model and feed-name map, rebuilding
// them only when the store has changed since the last build. Topic clustering
// over the corpus plus an all-feeds decode is ~40ms on a large database; both
// are pure functions of data that only changes on a write, so memoizing them
// against store.WriteGen() makes repeat page loads effectively free while
// staying current after any refresh, add, delete, or read/star toggle.
func (s *Server) frontView() (model *topics.Model, names map[string]string) {
	gen := s.store.WriteGen()

	s.frontMu.Lock()
	defer s.frontMu.Unlock()
	if s.frontValid && s.frontGen == gen {
		return s.frontModel, s.frontNames
	}

	arts, err := s.store.GetArticles("", frontCorpus)
	if err != nil {
		// On error, serve a stale cache if we have one rather than a blank
		// page; otherwise fall back to an empty model.
		if s.frontValid {
			return s.frontModel, s.frontNames
		}
		return topics.Build(nil, topicOptions()), map[string]string{}
	}

	model = topics.Build(arts, topicOptions())
	names = s.buildFeedNames()

	s.frontModel, s.frontNames, s.frontGen, s.frontValid = model, names, gen, true
	return model, names
}

// handleFront renders the composed newspaper: a scored lead and stable desk
// blocks with emergent topics as secondary labels. The edition is memoized by
// WriteGen so read/star state stays fresh without rebuilding per request.
func (s *Server) handleFront(w http.ResponseWriter, r *http.Request) {
	edition, names := s.editorialView()

	data := frontData{Now: time.Now(), HasContent: edition.Diagnostics.Selected > 0}
	data.Flash = takeFlash(w, r)
	if edition.Lead != nil {
		data.Lead = edition.Lead
		data.LeadFeed = names[edition.Lead.FeedID]
		data.LeadDeck = excerpt(articleBody(edition.Lead), deckLen)
		if desk, topic := edition.TopicForArticle(edition.Lead.ID); desk != nil {
			data.LeadDesk, data.LeadDeskSlug = desk.Desk.Label, desk.Desk.ID
			if topic != nil {
				data.LeadTopic = topic.Label
			}
		}
	}

	// Summarize the bounded, diversity-filtered edition rather than the
	// complete archive. Stable desks replace emergent terms as primary blocks.
	for _, section := range edition.Desks {
		if len(section.Articles) == 0 {
			continue
		}
		sv := sectionView{Slug: section.Desk.ID, Label: section.Desk.Label, Total: len(section.Articles)}
		for _, topic := range section.Topics {
			sv.Topics = append(sv.Topics, topicLink{Slug: topic.Slug, Label: topic.Label})
		}
		for _, article := range section.Articles {
			data.ArticleCount++
			if !article.Read {
				data.UnreadCount++
				sv.Unread++
			}
			if edition.Lead != nil && article.ID == edition.Lead.ID {
				if data.LeadDesk == "" {
					data.LeadDesk, data.LeadDeskSlug = section.Desk.Label, section.Desk.ID
				}
				continue
			}
			if sv.Spotlight == nil {
				sv.Spotlight = &headlineView{Article: article, Feed: names[article.FeedID], Deck: excerpt(articleBody(article), sectionDeckLen)}
				continue
			}
			if len(sv.Headlines) < headlinesPerSection-1 {
				sv.Headlines = append(sv.Headlines, headlineView{Article: article, Feed: names[article.FeedID]})
			}
		}
		shown := len(sv.Headlines)
		if sv.Spotlight != nil {
			shown++
		}
		totalWithoutLead := sv.Total
		if edition.Lead != nil && containsArticle(section.Articles, edition.Lead.ID) {
			totalWithoutLead--
		}
		sv.More = totalWithoutLead - shown
		if shown > 0 {
			data.Sections = append(data.Sections, sv)
		}
	}
	data.SourceCount = len(edition.Diagnostics.Sources)
	s.render(w, "front.html", data)
}

// handleTopic renders one topical section's full article list. The corpus
// and clustering match the front page, so a slug linked from the front
// page resolves to the same topic here.
func (s *Server) handleTopic(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	model, names := s.frontView()
	t := model.BySlug(slug)
	if t == nil {
		http.NotFound(w, r)
		return
	}
	data := topicData{Label: t.Label, Slug: t.Slug}
	for _, a := range t.Articles {
		data.Articles = append(data.Articles, headlineView{Article: a, Feed: names[a.FeedID]})
	}
	s.render(w, "topic.html", data)
}

// handleDesk renders a stable editorial desk. Empty desks remain valid URLs so
// navigation does not disappear with the current corpus.
func (s *Server) handleDesk(w http.ResponseWriter, r *http.Request) {
	edition, names := s.editorialView()
	section := edition.ByDesk(r.PathValue("slug"))
	if section == nil {
		http.NotFound(w, r)
		return
	}
	data := deskData{Desk: section.Desk}
	for _, topic := range section.Topics {
		data.Topics = append(data.Topics, topicLink{Slug: topic.Slug, Label: topic.Label})
	}
	for _, article := range section.Articles {
		data.Articles = append(data.Articles, headlineView{Article: article, Feed: names[article.FeedID]})
	}
	s.render(w, "desk.html", data)
}

// buildFeedNames maps feed ID to a display label (title, or host of the URL
// when untitled), for article bylines. Called only via frontView, which
// caches the result; do not call it per request directly.
func (s *Server) buildFeedNames() map[string]string {
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
	title := strings.TrimSpace(f.Title)
	// A degenerate title — empty or all-digits ("04", "2026") — is a useless
	// label (one real feed's title is literally "04"); prefer the host. Only
	// digits are rejected so legitimately short titles ("Go", "C") survive.
	if title != "" && !isAllDigits(title) {
		return title
	}
	if h := feedHost(f); h != "" {
		return h
	}
	if title != "" {
		return title
	}
	return f.URL
}

// isAllDigits reports whether s is non-empty and every rune is a digit.
func isAllDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return s != ""
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
	// Decode HTML entities the feed authored into its content (&mdash;, &amp;,
	// &#39;) so the deck reads as prose, not markup. stripTags only removes
	// <tags>; without this the lead story shows a literal "&mdash;".
	plain = html.UnescapeString(plain)
	plain = strings.Join(strings.Fields(plain), " ") // collapse whitespace
	runes := []rune(plain)
	if len(runes) <= n {
		return plain
	}
	cut := string(runes[:n])
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
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
