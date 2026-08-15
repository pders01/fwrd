package editorial

import (
	"math"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/topics"
)

// Options controls the explicit diversity policy used to compose an edition.
type Options struct {
	Now                time.Time
	MaxArticles        int
	MaxPerSource       int
	MaxPerDesk         int
	MaxPerSourceInDesk int
	TopicOptions       topics.Options
}

// DefaultOptions is the initial newspaper policy. The limits are deliberately
// exposed and covered by fixture diagnostics rather than hidden in a score:
// one source may occupy at most 24/400 stories globally and 10 in one desk;
// an assigned desk may occupy at most 60 stories. Unfiled may fill unused room
// so existing databases do not collapse to a tiny edition before assignment.
func DefaultOptions() Options {
	topicOpts := topics.DefaultOptions()
	return Options{
		MaxArticles:        400,
		MaxPerSource:       24,
		MaxPerDesk:         60,
		MaxPerSourceInDesk: 10,
		TopicOptions:       topicOpts,
	}
}

// Topic is an emergent event inside a stable desk. Promotion requires support
// from at least two source identities; single-source runs remain headlines.
type Topic struct {
	Slug        string
	Label       string
	Articles    []*storage.Article
	SourceCount int
}

// DeskSection is one stable newspaper desk in canonical order.
type DeskSection struct {
	Desk     Desk
	Articles []*storage.Article
	Topics   []Topic
}

// LeadScore exposes every lead-ranking component. Total is
// RecencyPoints + 12*TopicSources + 2*min(DeskSources,5) + StarredPoints.
type LeadScore struct {
	Article       *storage.Article
	RecencyPoints int
	TopicSources  int
	DeskSources   int
	StarredPoints int
	Total         int
}

// Share is a diagnostic count and fraction of the selected edition.
type Share struct {
	ID    string
	Label string
	Count int
	Share float64
}

// TopicDiagnostic makes source support inspectable while tuning fixtures and
// a live corpus.
type TopicDiagnostic struct {
	DeskID      string
	Slug        string
	Label       string
	Articles    int
	SourceCount int
}

// Diagnostics reports the composition signals called out by the editorial
// policy: source share, desk share, topic support, and unfiled fallback share.
type Diagnostics struct {
	Selected      int
	Candidates    int
	Sources       []Share
	Desks         []Share
	Topics        []TopicDiagnostic
	FallbackCount int
	FallbackShare float64
}

// Edition is a deterministic composition of stable desks and emergent topics.
type Edition struct {
	Lead        *storage.Article
	LeadScore   LeadScore
	Desks       []DeskSection
	Diagnostics Diagnostics
	byDesk      map[string]*DeskSection
}

// Build composes articles using explicit feed desk metadata. Input order does
// not matter. Callers should provide a candidate window larger than
// MaxArticles so diversity limits can skip prolific sources before the edition
// is filled.
func Build(articles []*storage.Article, feeds []*storage.Feed, opts Options) *Edition {
	opts = normalizeOptions(opts)
	if opts.Now.IsZero() {
		// A zero reference means "no future check" without introducing wall
		// clock nondeterminism: score relative to the freshest dated input.
		for _, article := range articles {
			if article != nil && article.Published.After(opts.Now) {
				opts.Now = article.Published
			}
		}
	}
	edition := &Edition{
		byDesk: make(map[string]*DeskSection),
		Desks:  make([]DeskSection, 0, len(stableDesks)),
	}
	feedByID := make(map[string]*storage.Feed, len(feeds))
	for _, feed := range feeds {
		if feed != nil {
			feedByID[feed.ID] = feed
		}
	}

	ordered := append([]*storage.Article(nil), articles...)
	sortArticles(ordered, opts.Now)
	edition.Diagnostics.Candidates = len(ordered)

	sourceCounts := map[string]int{}
	deskCounts := map[string]int{}
	deskSourceCounts := map[string]map[string]int{}
	selectedByDesk := map[string][]*storage.Article{}
	for _, article := range ordered {
		if article == nil {
			continue
		}
		if edition.Diagnostics.Selected >= opts.MaxArticles {
			break
		}
		feed := feedByID[article.FeedID]
		deskID, ok := NormalizeDeskID(feedDeskID(feed))
		if !ok {
			deskID = DeskUnfiled
		}
		source := sourceKey(feed, article.FeedID)
		if sourceCounts[source] >= opts.MaxPerSource {
			continue
		}
		if deskID != DeskUnfiled && deskCounts[deskID] >= opts.MaxPerDesk {
			continue
		}
		if deskSourceCounts[deskID] == nil {
			deskSourceCounts[deskID] = map[string]int{}
		}
		if deskSourceCounts[deskID][source] >= opts.MaxPerSourceInDesk {
			continue
		}
		selectedByDesk[deskID] = append(selectedByDesk[deskID], article)
		sourceCounts[source]++
		deskCounts[deskID]++
		deskSourceCounts[deskID][source]++
		edition.Diagnostics.Selected++
	}

	articleTopicSources := map[string]int{}
	for _, desk := range Desks() {
		section := DeskSection{Desk: desk, Articles: selectedByDesk[desk.ID]}
		if len(section.Articles) > 0 {
			topicOpts := opts.TopicOptions
			topicOpts.Now = opts.Now
			model := topics.Build(section.Articles, topicOpts)
			for _, candidate := range model.Topics {
				if len(candidate.Terms) == 0 {
					continue
				}
				sources := map[string]bool{}
				for _, article := range candidate.Articles {
					sources[sourceKey(feedByID[article.FeedID], article.FeedID)] = true
				}
				if len(sources) < 2 {
					continue
				}
				topic := Topic{Slug: candidate.Slug, Label: candidate.Label, Articles: candidate.Articles, SourceCount: len(sources)}
				section.Topics = append(section.Topics, topic)
				for _, article := range topic.Articles {
					articleTopicSources[article.ID] = topic.SourceCount
				}
				edition.Diagnostics.Topics = append(edition.Diagnostics.Topics, TopicDiagnostic{
					DeskID: desk.ID, Slug: topic.Slug, Label: topic.Label,
					Articles: len(topic.Articles), SourceCount: topic.SourceCount,
				})
			}
		}
		edition.Desks = append(edition.Desks, section)
		edition.byDesk[desk.ID] = &edition.Desks[len(edition.Desks)-1]
	}

	edition.chooseLead(opts.Now, deskSourceCounts, articleTopicSources)
	edition.buildDiagnostics(sourceCounts, deskCounts)
	return edition
}

func normalizeOptions(opts Options) Options {
	defaults := DefaultOptions()
	if opts.MaxArticles <= 0 {
		opts.MaxArticles = defaults.MaxArticles
	}
	if opts.MaxPerSource <= 0 {
		opts.MaxPerSource = defaults.MaxPerSource
	}
	if opts.MaxPerDesk <= 0 {
		opts.MaxPerDesk = defaults.MaxPerDesk
	}
	if opts.MaxPerSourceInDesk <= 0 {
		opts.MaxPerSourceInDesk = defaults.MaxPerSourceInDesk
	}
	if opts.TopicOptions.MaxTermsPerDoc <= 0 {
		opts.TopicOptions = defaults.TopicOptions
	}
	return opts
}

func (e *Edition) chooseLead(now time.Time, deskSources map[string]map[string]int, topicSources map[string]int) {
	var candidates []LeadScore
	for _, section := range e.Desks {
		for _, article := range section.Articles {
			if article.Published.IsZero() || article.Published.After(now) {
				continue
			}
			ageHours := int(now.Sub(article.Published).Hours())
			recency := max(0, 48-ageHours)
			deskBreadth := len(deskSources[section.Desk.ID])
			starred := 0
			if article.Starred {
				starred = 6
			}
			score := LeadScore{
				Article: article, RecencyPoints: recency,
				TopicSources: topicSources[article.ID], DeskSources: deskBreadth,
				StarredPoints: starred,
			}
			score.Total = score.RecencyPoints + 12*score.TopicSources + 2*min(score.DeskSources, 5) + score.StarredPoints
			candidates = append(candidates, score)
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Total != candidates[j].Total {
			return candidates[i].Total > candidates[j].Total
		}
		pi, pj := candidates[i].Article.Published, candidates[j].Article.Published
		if !pi.Equal(pj) {
			return pi.After(pj)
		}
		return candidates[i].Article.ID < candidates[j].Article.ID
	})
	if len(candidates) > 0 {
		e.LeadScore = candidates[0]
		e.Lead = candidates[0].Article
	}
}

func (e *Edition) buildDiagnostics(sources, desks map[string]int) {
	total := e.Diagnostics.Selected
	for id, count := range sources {
		e.Diagnostics.Sources = append(e.Diagnostics.Sources, Share{ID: id, Label: id, Count: count, Share: fraction(count, total)})
	}
	sort.Slice(e.Diagnostics.Sources, func(i, j int) bool {
		if e.Diagnostics.Sources[i].Count != e.Diagnostics.Sources[j].Count {
			return e.Diagnostics.Sources[i].Count > e.Diagnostics.Sources[j].Count
		}
		return e.Diagnostics.Sources[i].ID < e.Diagnostics.Sources[j].ID
	})
	for _, desk := range Desks() {
		count := desks[desk.ID]
		e.Diagnostics.Desks = append(e.Diagnostics.Desks, Share{ID: desk.ID, Label: desk.Label, Count: count, Share: fraction(count, total)})
	}
	e.Diagnostics.FallbackCount = desks[DeskUnfiled]
	e.Diagnostics.FallbackShare = fraction(e.Diagnostics.FallbackCount, total)
}

// ByDesk returns a stable desk, including an empty one, or nil for an unknown
// slug.
func (e *Edition) ByDesk(id string) *DeskSection {
	if e == nil {
		return nil
	}
	normalized, ok := NormalizeDeskID(id)
	if !ok {
		return nil
	}
	return e.byDesk[normalized]
}

// TopicForArticle returns the promoted topic containing articleID within its
// desk, if any.
func (e *Edition) TopicForArticle(articleID string) (*DeskSection, *Topic) {
	if e == nil {
		return nil, nil
	}
	for i := range e.Desks {
		for j := range e.Desks[i].Topics {
			for _, article := range e.Desks[i].Topics[j].Articles {
				if article.ID == articleID {
					return &e.Desks[i], &e.Desks[i].Topics[j]
				}
			}
		}
	}
	return nil, nil
}

func feedDeskID(feed *storage.Feed) string {
	if feed == nil {
		return ""
	}
	return feed.DeskID
}

// sourceKey groups category feeds from one publication by host. Explicit
// source identity can replace this fallback later without changing the desk
// taxonomy; domains are never used to assign desks.
func sourceKey(feed *storage.Feed, fallback string) string {
	if feed != nil {
		if parsed, err := url.Parse(feed.URL); err == nil {
			host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
			if host != "" {
				return host
			}
		}
		if feed.ID != "" {
			return feed.ID
		}
	}
	return fallback
}

func sortArticles(articles []*storage.Article, now time.Time) {
	if now.IsZero() {
		now = time.Unix(1<<62, 0)
	}
	rank := func(article *storage.Article) int64 {
		if article == nil || article.Published.IsZero() || article.Published.After(now) {
			return math.MinInt64
		}
		return article.Published.Unix()
	}
	sort.SliceStable(articles, func(i, j int) bool {
		ri, rj := rank(articles[i]), rank(articles[j])
		if ri != rj {
			return ri > rj
		}
		if articles[i] == nil {
			return false
		}
		if articles[j] == nil {
			return true
		}
		return articles[i].ID < articles[j].ID
	})
}

func fraction(count, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(count) / float64(total)
}
