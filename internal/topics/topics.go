// Package topics groups articles into emergent topical sections using a
// TF-IDF signal over the corpus. It powers the web view's newspaper front
// page (section blocks) and the per-topic pages.
//
// The model is deliberately dependency-free and deterministic: the same
// articles always yield the same topics and ordering, which keeps slugs
// stable across requests and makes the clustering testable. It reads the
// stored articles directly rather than coupling to the search index, so it
// works regardless of which search backend is active.
package topics

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/pders01/fwrd/internal/storage"
)

// Options tunes the clustering. The zero value is not useful; callers
// should use DefaultOptions and adjust from there.
type Options struct {
	// MaxTermsPerDoc caps how many high-TF-IDF terms represent an article
	// when looking for shared topics.
	MaxTermsPerDoc int
	// Now is the reference time for recency ranking. Articles published
	// after it (scheduled/mis-dated future items) and undated (zero-time)
	// articles are ranked stale, so they neither lead nor dominate a
	// section. The zero value disables the future check but still sinks
	// undated articles.
	Now time.Time
	// MinTopicSize is the fewest articles a shared term must gather before
	// it is promoted to a topic. Below this, a term is too idiosyncratic
	// to be a section.
	MinTopicSize int
	// MaxTopics bounds how many topical sections are formed; the rest of
	// the articles fall into the catch-all section.
	MaxTopics int
	// MaxDocFraction drops terms that appear in more than this fraction of
	// the corpus — boilerplate ("comments", "posted") that would otherwise
	// dominate and merge unrelated articles.
	MaxDocFraction float64
	// CatchAllLabel names the section holding articles that share no
	// significant term with enough others to form a topic.
	CatchAllLabel string
}

// DefaultOptions returns sensible defaults tuned for a tech/science feed
// reading list of a few hundred recent articles.
func DefaultOptions() Options {
	return Options{
		MaxTermsPerDoc: 8,
		MinTopicSize:   3,
		MaxTopics:      12,
		MaxDocFraction: 0.5,
		CatchAllLabel:  "Latest",
	}
}

// Topic is one section: a label, a stable slug, the terms that defined it,
// and its articles in newest-first order.
type Topic struct {
	Slug     string
	Label    string
	Terms    []string
	Articles []*storage.Article
}

// Model is the result of clustering: the lead story (most recent across
// the whole corpus) and the topical sections.
type Model struct {
	Lead   *storage.Article
	Topics []Topic
	bySlug map[string]*Topic
}

// BySlug returns the topic with the given slug, or nil if none matches.
func (m *Model) BySlug(slug string) *Topic {
	if m == nil {
		return nil
	}
	return m.bySlug[slug]
}

// doc holds the per-article working state during clustering.
type doc struct {
	article   *storage.Article
	signature []string // significant terms, highest TF-IDF first
	topic     int      // assigned topic index, -1 until claimed
}

// Build clusters articles into a Model. articles are expected newest-first
// (as the store returns them); the first is taken as the lead story. A nil
// or empty slice yields an empty model.
func Build(articles []*storage.Article, opts Options) *Model {
	m := &Model{bySlug: map[string]*Topic{}}
	if len(articles) == 0 {
		return m
	}

	rank := rankFunc(opts.Now)
	byRank := func(a []*storage.Article) {
		sort.SliceStable(a, func(i, j int) bool {
			ri, rj := rank(a[i]), rank(a[j])
			if ri != rj {
				return ri > rj
			}
			return a[i].ID < a[j].ID
		})
	}

	// Work from a recency-ordered copy. The store's date index sorts
	// undated (zero-time) articles first, which would otherwise make a
	// feed "about" page the lead; ranking sinks those and future-dated
	// items so the lead is the newest genuinely-published article.
	ordered := append([]*storage.Article(nil), articles...)
	byRank(ordered)
	m.Lead = ordered[0]

	n := len(ordered)
	docs := make([]*doc, n)
	df := map[string]int{} // document frequency
	tfs := make([]map[string]int, n)

	for i, a := range ordered {
		counts := tokenize(docText(a))
		tfs[i] = counts
		for term := range counts {
			df[term]++
		}
		docs[i] = &doc{article: a, topic: -1}
	}

	// Drop terms appearing in more than MaxDocFraction of the corpus as
	// boilerplate — but never below MinTopicSize, or on a small corpus the
	// fraction cap could forbid any topic from forming at all.
	maxDF := max(int(math.Floor(opts.MaxDocFraction*float64(n))), opts.MinTopicSize)

	// Per-doc signature: top MaxTermsPerDoc terms by TF-IDF, keeping only
	// terms shared by at least two documents (df >= 2) and not boilerplate
	// (df <= maxDF). A term unique to one article can't seed a topic.
	for i := range docs {
		type scored struct {
			term  string
			score float64
		}
		var cand []scored
		for term, tf := range tfs[i] {
			d := df[term]
			if d < 2 || d > maxDF {
				continue
			}
			idf := math.Log(float64(n) / float64(d))
			cand = append(cand, scored{term, float64(tf) * idf})
		}
		sort.Slice(cand, func(a, b int) bool {
			if cand[a].score != cand[b].score {
				return cand[a].score > cand[b].score
			}
			return cand[a].term < cand[b].term // deterministic tie-break
		})
		if len(cand) > opts.MaxTermsPerDoc {
			cand = cand[:opts.MaxTermsPerDoc]
		}
		sig := make([]string, len(cand))
		for j, c := range cand {
			sig[j] = c.term
		}
		docs[i].signature = sig
	}

	// Rank candidate seed terms by their connective strength: how many
	// documents carry the term, weighted by its idf so a term shared by
	// many yet still distinctive outranks a merely frequent one.
	termDocs := map[string][]int{}
	for i, d := range docs {
		for _, term := range d.signature {
			termDocs[term] = append(termDocs[term], i)
		}
	}
	type seed struct {
		term  string
		score float64
	}
	var seeds []seed
	for term, idxs := range termDocs {
		if len(idxs) < opts.MinTopicSize {
			continue
		}
		idf := math.Log(float64(n) / float64(df[term]))
		seeds = append(seeds, seed{term, float64(len(idxs)) * idf})
	}
	sort.Slice(seeds, func(a, b int) bool {
		if seeds[a].score != seeds[b].score {
			return seeds[a].score > seeds[b].score
		}
		return seeds[a].term < seeds[b].term
	})

	// Greedy assignment: walk seeds strongest-first, claiming as-yet
	// unassigned documents carrying the term. A seed becomes a topic only
	// if it still gathers MinTopicSize documents after earlier topics took
	// theirs — so each article lands in exactly one section.
	for _, s := range seeds {
		if len(m.Topics) >= opts.MaxTopics {
			break
		}
		var claimed []*storage.Article
		var claimedIdx []int
		for _, i := range termDocs[s.term] {
			if docs[i].topic == -1 {
				claimedIdx = append(claimedIdx, i)
				claimed = append(claimed, docs[i].article)
			}
		}
		if len(claimed) < opts.MinTopicSize {
			continue
		}
		idx := len(m.Topics)
		for _, i := range claimedIdx {
			docs[i].topic = idx
		}
		byRank(claimed)
		m.Topics = append(m.Topics, Topic{
			Label:    label(s.term),
			Terms:    []string{s.term},
			Articles: claimed,
		})
	}

	// Everything still unassigned forms the catch-all "Latest" section.
	var rest []*storage.Article
	for _, d := range docs {
		if d.topic == -1 {
			rest = append(rest, d.article)
		}
	}
	if len(rest) > 0 {
		byRank(rest)
		m.Topics = append(m.Topics, Topic{
			Label:    opts.CatchAllLabel,
			Articles: rest,
		})
	}

	// Order sections by the recency of their freshest article so the most
	// active topics lead the page, mirroring a newspaper's front. Each
	// topic's articles are already rank-sorted, so element 0 is freshest;
	// a topic of only undated/future items sinks to the bottom.
	topicRank := func(t Topic) int64 {
		if len(t.Articles) == 0 {
			return math.MinInt64
		}
		return rank(t.Articles[0])
	}
	// Named topics lead, ordered by recency; the catch-all (no defining
	// terms) is always last, however fresh — it is the "everything else"
	// bin, not a headline section.
	sort.SliceStable(m.Topics, func(a, b int) bool {
		ca, cb := len(m.Topics[a].Terms) == 0, len(m.Topics[b].Terms) == 0
		if ca != cb {
			return !ca
		}
		return topicRank(m.Topics[a]) > topicRank(m.Topics[b])
	})

	// Assign unique slugs after final ordering and index them.
	used := map[string]bool{}
	for i := range m.Topics {
		s := uniqueSlug(slugify(m.Topics[i].Label), used)
		m.Topics[i].Slug = s
		used[s] = true
		m.bySlug[s] = &m.Topics[i]
	}
	return m
}

// rankFunc returns a recency score for an article: its publish time as a
// Unix timestamp, or math.MinInt64 ("stale") when the article is undated
// or published after now. A zero now disables the future check (nothing is
// future) while still sinking undated articles.
func rankFunc(now time.Time) func(*storage.Article) int64 {
	effNow := now
	if effNow.IsZero() {
		effNow = time.Unix(1<<62, 0)
	}
	return func(a *storage.Article) int64 {
		if a.Published.IsZero() || a.Published.After(effNow) {
			return math.MinInt64
		}
		return a.Published.Unix()
	}
}

// docText assembles the text used for a document's term signature: the
// title and description. Body content is deliberately excluded — for
// aggregator feeds (Tildes, Hacker News) it is mostly link boilerplate,
// which leaks domain tokens ("tildes", "ycombinator") into spurious
// topics that are worse than the few articles it would rescue.
func docText(a *storage.Article) string {
	return a.Title + " " + plain(a.Description)
}

// label turns a seed term into a section heading: Title Case.
func label(term string) string {
	if term == "" {
		return ""
	}
	r := []rune(term)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func slugify(s string) string {
	var b strings.Builder
	prevDash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			prevDash = false
		case !prevDash:
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func uniqueSlug(base string, used map[string]bool) string {
	if base == "" {
		base = "section"
	}
	if !used[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := base + "-" + itoa(i)
		if !used[cand] {
			return cand
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
