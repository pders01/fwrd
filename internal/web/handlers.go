package web

import (
	"html/template"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pders01/fwrd/internal/opml"
	"github.com/pders01/fwrd/internal/search"
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
	Unread int
	Total  int
}

type indexData struct {
	Feeds []feedView
}

type feedData struct {
	Feed       *storage.Feed
	Articles   []*storage.Article
	NextCursor string
}

type articleData struct {
	Article *storage.Article
	Feed    *storage.Feed
	Body    template.HTML
}

type searchData struct {
	Query     string
	Results   []*storage.Article
	Available bool
}

// handleFeeds renders the feed-management page: the full feed list with
// unread counts plus the add / refresh / delete / OPML controls. The
// reading surface (the newspaper front page) lives at "/"; this is the
// "manage" half of the read/manage split.
func (s *Server) handleFeeds(w http.ResponseWriter, _ *http.Request) {
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
			return feeds[i].URL < feeds[j].URL
		}
		return strings.ToLower(li) < strings.ToLower(lj)
	})
	views := make([]feedView, 0, len(feeds))
	for _, f := range feeds {
		unread, total := s.feedCounts(f.ID)
		views = append(views, feedView{
			Feed:   f,
			Label:  feedLabel(f),
			Source: feedSource(f),
			Unread: unread,
			Total:  total,
		})
	}
	s.render(w, "feeds.html", indexData{Feeds: views})
}

// feedCounts returns the unread and total article counts for a feed in a
// single scan. Feeds are bounded in size so this stays cheap. Errors
// collapse to zero — a count is not worth failing a page render over.
func (s *Server) feedCounts(feedID string) (unread, total int) {
	articles, err := s.store.GetArticles(feedID, 0)
	if err != nil {
		return 0, 0
	}
	total = len(articles)
	for _, a := range articles {
		if !a.Read {
			unread++
		}
	}
	return unread, total
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
	s.render(w, "feed.html", feedData{Feed: feed, Articles: articles, NextCursor: next})
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
	s.render(w, "article.html", articleData{Article: article, Feed: feed, Body: body})
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
		for _, res := range results {
			if res.Article != nil {
				data.Results = append(data.Results, res.Article)
			}
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
	url := strings.TrimSpace(r.FormValue("url"))
	if url == "" {
		http.Error(w, "missing feed url", http.StatusBadRequest)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.manager.AddFeed(url); err != nil {
		http.Error(w, "failed to add feed: "+err.Error(), http.StatusBadGateway)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) handleRefreshFeed(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.manager.RefreshFeed(id); err != nil {
		http.Error(w, "failed to refresh feed: "+err.Error(), http.StatusBadGateway)
		return
	}
	redirect(w, r, "/feeds/"+id)
}

func (s *Server) handleRefreshAll(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if _, err := s.manager.RefreshAllFeeds(); err != nil {
		http.Error(w, "failed to refresh feeds: "+err.Error(), http.StatusBadGateway)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) handleDeleteFeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if err := s.store.DeleteFeed(id); err != nil {
		http.Error(w, "failed to delete feed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// Keep the search index in step with the deletion.
	if dl, ok := s.searcher.(search.DeleteListener); ok {
		dl.OnFeedDeleted(id)
	}
	redirect(w, r, "/")
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
// holds writeMu like the other manager-driven mutations.
func (s *Server) handleOPMLImport(w http.ResponseWriter, r *http.Request) {
	if s.manager == nil {
		http.Error(w, "feed management is disabled", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseMultipartForm(opmlMaxUpload); err != nil {
		http.Error(w, "invalid upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing OPML file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	feeds, err := opml.Parse(file)
	if err != nil {
		http.Error(w, "failed to parse OPML: "+err.Error(), http.StatusBadRequest)
		return
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	existing, _ := s.store.GetAllFeeds()
	have := make(map[string]bool, len(existing))
	for _, f := range existing {
		have[f.URL] = true
	}
	for _, f := range feeds {
		if have[f.URL] {
			continue
		}
		// Best-effort: a feed that fails to fetch is skipped so one bad
		// entry doesn't abort the whole import.
		_, _ = s.manager.AddFeed(f.URL)
	}
	redirect(w, r, "/")
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
	if strings.HasPrefix(p, "/") && !strings.HasPrefix(p, "//") {
		return p
	}
	return "/"
}
