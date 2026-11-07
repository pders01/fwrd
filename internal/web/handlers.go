package web

import (
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/pders01/fwrd/internal/editorial"
	"github.com/pders01/fwrd/internal/opml"
	"github.com/pders01/fwrd/internal/storage"
)

// opmlMaxUpload bounds the size of an uploaded OPML file held in memory
// while parsing. Subscription lists are small; this is a generous ceiling
// that still rejects a hostile multi-megabyte upload.
const opmlMaxUpload = 2 << 20 // 2 MiB

// articlesPerPage bounds how many articles a feed page renders before
// offering a "next" link. The store supports cursor pagination, so deep
// feeds stay responsive.
const articlesPerPage = 50

type feedView struct {
	Feed   *storage.Feed
	Label  string
	Source string
	Desk   editorial.Desk
	Unread int
	Total  int
}

type indexData struct {
	pageBase
	Feeds         []feedView
	TotalArticles int
	TotalUnread   int
	FailedFeeds   int
}

type feedData struct {
	pageBase
	Feed        *storage.Feed
	Label       string
	Source      string
	FetchSource string
	Desk        editorial.Desk
	Desks       []editorial.Desk
	Articles    []*storage.Article
	Total       int
	Unread      int
	NextCursor  string
}

type articleData struct {
	pageBase
	Article    *storage.Article
	Feed       *storage.Feed
	FeedLabel  string
	FeedSource string
	Body       template.HTML
}

type searchData struct {
	pageBase
	Query     string
	Results   []headlineView
	Available bool
}

// handleFeeds renders the feed-management page: the full feed list with
// unread counts plus the add / refresh / delete / OPML controls. The
// reading surface (the newspaper front page) lives at "/"; this is the
// "manage" half of the read/manage split.
func (s *Server) handleFeeds(w http.ResponseWriter, r *http.Request) {
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		http.Error(w, "failed to load feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Sort by display label (case-insensitive); identical labels — e.g.
	// three untitled arxiv.org feeds — break to URL so duplicates land
	// adjacent and in a stable order.
	sort.Slice(feeds, func(i, j int) bool {
		li, lj := feedLabel(feeds[i]), feedLabel(feeds[j])
		if strings.EqualFold(li, lj) {
			return feeds[i].SubscriptionURL() < feeds[j].SubscriptionURL()
		}
		return strings.ToLower(li) < strings.ToLower(lj)
	})
	// One transaction yields every feed's counts via index KeyN (no article
	// JSON decoded), replacing a per-feed decode-and-sort of the whole corpus.
	stats, err := s.store.FeedStats()
	if err != nil {
		http.Error(w, "failed to load feed stats: "+err.Error(), http.StatusInternalServerError)
		return
	}
	views := make([]feedView, 0, len(feeds))
	data := indexData{}
	for _, f := range feeds {
		st := stats[f.ID]
		desk, _ := editorial.DeskByID(f.DeskID)
		views = append(views, feedView{
			Feed:   f,
			Label:  feedLabel(f),
			Source: feedSource(f),
			Desk:   desk,
			Unread: st.Unread,
			Total:  st.Total,
		})
		data.TotalArticles += st.Total
		data.TotalUnread += st.Unread
		if f.LastError != "" {
			data.FailedFeeds++
		}
	}
	data.Feeds = views
	data.Flash = takeFlash(w, r)
	s.render(w, "feeds.html", data)
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	feed, err := s.store.GetFeed(id)
	if err != nil || feed == nil {
		http.NotFound(w, r)
		return
	}
	cursor := r.URL.Query().Get("cursor")
	// Fetch one extra to detect whether a further page exists.
	articles, err := s.store.GetArticlesWithCursor(id, articlesPerPage+1, cursor)
	if err != nil {
		http.Error(w, "failed to load articles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	next := ""
	if len(articles) > articlesPerPage {
		articles = articles[:articlesPerPage]
		next = articles[len(articles)-1].ID
	}
	stat := storage.FeedStat{}
	if stats, statErr := s.store.FeedStats(); statErr == nil {
		stat = stats[id]
	}
	desk, _ := editorial.DeskByID(feed.DeskID)
	data := feedData{
		Feed:        feed,
		Label:       feedLabel(feed),
		Source:      feedSource(feed),
		FetchSource: feedFetchSource(feed),
		Desk:        desk,
		Desks:       editorial.Desks(),
		Articles:    articles,
		Total:       stat.Total,
		Unread:      stat.Unread,
		NextCursor:  next,
	}
	data.Flash = takeFlash(w, r)
	s.render(w, "feed.html", data)
}

func (s *Server) handleArticle(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	article, err := s.store.GetArticle(id)
	if err != nil || article == nil {
		http.NotFound(w, r)
		return
	}
	// Feed lookup is best-effort: render the article even if the parent
	// feed record has gone missing.
	feed, _ := s.store.GetFeed(article.FeedID)
	body := template.HTML(getSanitizer().Sanitize(articleBody(article))) //nolint:gosec // sanitized
	data := articleData{Article: article, Feed: feed, Body: body}
	if feed != nil {
		data.FeedLabel = feedLabel(feed)
		data.FeedSource = feedSource(feed)
	}
	s.render(w, "article.html", data)
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	data := searchData{Query: q, Available: s.searcher != nil}
	if s.searcher != nil && q != "" {
		results, err := s.searcher.Search(q, articlesPerPage)
		if err != nil {
			http.Error(w, "search failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		// The search index stores a thin article (no Published, read/star
		// state, or feed); hydrate the full record from storage so each result
		// carries a source byline and date — the only way to tell near-
		// identical titles ("This Week in Rust 650/648/651") apart. names is
		// the memoized feed-ID → label map shared with the front page.
		_, names := s.frontView()
		for _, res := range results {
			if res.Article == nil {
				continue
			}
			a := res.Article
			if full, err := s.store.GetArticle(a.ID); err == nil && full != nil {
				a = full
			}
			data.Results = append(data.Results, headlineView{Article: a, Feed: names[a.FeedID]})
		}
	}
	s.render(w, "search.html", data)
}

// --- mutating handlers (POST, behind same-origin guard) ---

func (s *Server) handleAddFeed(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	feedURL := strings.TrimSpace(r.FormValue("url"))
	if feedURL == "" {
		setFlash(w, flashError, "Enter a feed URL to add.")
		redirect(w, r, "/feeds")
		return
	}
	f, err := s.addFeed(r.Context(), feedURL)
	if err != nil {
		setFlash(w, flashError, "Couldn't add "+feedURL+": "+err.Error())
		redirect(w, r, "/feeds")
		return
	}
	setFlash(w, flashNotice, "Added "+feedLabel(f)+".")
	redirect(w, r, "/feeds")
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := s.refreshFeed(r.Context(), id); err != nil {
		// The feed page shows the persisted error badge; the flash names it.
		setFlash(w, flashError, "Refresh failed: "+err.Error())
		redirect(w, r, "/feeds/"+id)
		return
	}
	setFlash(w, flashNotice, "Feed refreshed.")
	redirect(w, r, "/feeds/"+id)
}

func (s *Server) handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	// Per-feed failures are expected (feeds go down) and are persisted as
	// badges on /feeds, so a partial failure is not a page error — summarize
	// it in a flash instead of replacing the UI with a raw 502.
	summary, err := s.refreshAllFeeds(r.Context())
	switch {
	case len(summary.Errors) == 0 && err != nil:
		// No per-feed errors but a returned error means a catastrophic
		// failure (e.g. listing the feeds failed), which is page-worthy.
		setFlash(w, flashError, "Refresh failed: "+err.Error())
	case len(summary.Errors) > 0:
		setFlash(w, flashError, fmt.Sprintf(
			"Refreshed %d feed(s), %d new; %d failed — see the Feeds page.",
			summary.UpdatedFeeds, summary.AddedArticles, len(summary.Errors)))
	default:
		setFlash(w, flashNotice, fmt.Sprintf(
			"Refreshed %d feed(s), %d new article(s).",
			summary.UpdatedFeeds, summary.AddedArticles))
	}
	redirect(w, r, "/")
}

func (s *Server) handleSetFeedDesk(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	deskID, ok := editorial.NormalizeDeskID(r.FormValue("desk"))
	if !ok {
		setFlash(w, flashError, "Unknown desk.")
		redirect(w, r, "/feeds/"+id)
		return
	}
	feed, err := s.setFeedDesk(r.Context(), id, deskID)
	if err != nil {
		setFlash(w, flashError, "Couldn't update desk: "+err.Error())
		redirect(w, r, "/feeds/"+id)
		return
	}
	desk, _ := editorial.DeskByID(deskID)
	setFlash(w, flashNotice, "Filed "+feedLabel(feed)+" under "+desk.Label+".")
	redirect(w, r, "/feeds/"+id)
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.deleteFeed(r.Context(), id); err != nil {
		setFlash(w, flashError, "Couldn't delete feed: "+err.Error())
		redirect(w, r, "/feeds")
		return
	}
	setFlash(w, flashNotice, "Feed deleted.")
	redirect(w, r, "/feeds")
}

func (s *Server) handleToggleRead(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "missing article id", http.StatusBadRequest)
		return
	}
	read := r.FormValue("read") == "1"
	if err := s.store.MarkArticleRead(id, read); err != nil {
		http.Error(w, "failed to update article: "+err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, safeReturn(r.FormValue("return")))
}

// handleOPMLExport streams all feeds as an OPML attachment. It is a GET so
// it can be a plain link; no state changes, so the same-origin guard does
// not apply.
func (s *Server) handleOPMLExport(w http.ResponseWriter, _ *http.Request) {
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		http.Error(w, "failed to load feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	data, err := opml.Export(feeds, time.Now())
	if err != nil {
		http.Error(w, "failed to render OPML: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/x-opml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="fwrd-feeds.opml"`)
	_, _ = w.Write(data)
}

// handleOPMLImport accepts an uploaded OPML file and adds each listed feed.
// Like the CLI importer it skips feeds already present and reports failures
// without aborting the rest. AddFeed notifies the search index, so this
// uses the coordinator for mutation serialization.
func (s *Server) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	// mutationForm has bounded and parsed the body and owns multipart cleanup.
	file, _, err := r.FormFile("file")
	if err != nil {
		setFlash(w, flashError, "Choose an OPML file to import.")
		redirect(w, r, "/feeds")
		return
	}
	defer file.Close()

	feeds, err := opml.Parse(file)
	if err != nil {
		setFlash(w, flashError, "Couldn't parse OPML: "+err.Error())
		redirect(w, r, "/feeds")
		return
	}

	urls := make([]string, len(feeds))
	for i, f := range feeds {
		urls[i] = f.URL
	}
	added, skipped, failed, importErr := s.importFeeds(r.Context(), urls)
	if importErr != nil {
		setFlash(w, flashError, "Import failed: "+importErr.Error())
		redirect(w, r, "/feeds")
		return
	}
	metadataErr := s.applyImportedDeskMetadata(r, feeds)
	msg := fmt.Sprintf("Imported %d feed(s)", added)
	if skipped > 0 {
		msg += fmt.Sprintf(", %d already present", skipped)
	}
	switch {
	case failed > 0:
		setFlash(w, flashError, msg+fmt.Sprintf(", %d failed to fetch.", failed))
	case metadataErr != nil:
		setFlash(w, flashError, msg+"; couldn't restore desk assignments: "+metadataErr.Error())
	default:
		setFlash(w, flashNotice, msg+".")
	}
	redirect(w, r, "/feeds")
}

func (s *Server) applyImportedDeskMetadata(r *http.Request, imported []opml.Feed) error {
	stored, err := s.store.GetAllFeeds()
	if err != nil {
		return err
	}
	byURL := make(map[string]*storage.Feed, 2*len(stored))
	for _, feed := range stored {
		byURL[feed.URL] = feed
		byURL[feed.SubscriptionURL()] = feed
	}
	for _, item := range imported {
		if item.DeskID == "" {
			continue
		}
		feed := byURL[item.URL]
		if feed == nil {
			continue
		}
		// The snapshot is only an identity lookup. Apply the assignment to
		// the current record atomically, even for repeated OPML entries.
		if _, err := s.setFeedDesk(r.Context(), feed.ID, item.DeskID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) handleToggleStar(w http.ResponseWriter, r *http.Request) {
	id := r.FormValue("id")
	if id == "" {
		http.Error(w, "missing article id", http.StatusBadRequest)
		return
	}
	starred := r.FormValue("starred") == "1"
	if err := s.store.MarkArticleStarred(id, starred); err != nil {
		http.Error(w, "failed to update article: "+err.Error(), http.StatusInternalServerError)
		return
	}
	redirect(w, r, safeReturn(r.FormValue("return")))
}

// redirect issues a Post/Redirect/Get 303 so a browser refresh won't
// re-submit the form.
func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// safeReturn constrains a caller-supplied redirect target to a local path,
// preventing the form from being abused as an open redirect. Anything not
// starting with a single leading slash falls back to the index.
func safeReturn(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return "/"
	}
	// Browsers treat backslashes as slashes in special URLs and discard
	// some control bytes. Reject both raw and encoded forms rather than
	// relying only on net/url's (non-browser) interpretation.
	decoded, err := url.PathUnescape(p)
	if err != nil {
		return "/"
	}
	for _, value := range []string{p, decoded} {
		if strings.ContainsRune(value, '\\') || strings.ContainsFunc(value, func(c rune) bool { return c < 0x20 || c == 0x7f }) {
			return "/"
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.IsAbs() || u.Host != "" || strings.HasPrefix(u.Path, "//") {
		return "/"
	}
	return p
}
