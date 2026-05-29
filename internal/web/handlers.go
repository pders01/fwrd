package web

import (
	"html/template"
	"net/http"
	"sort"
	"strings"

	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

// articlesPerPage bounds how many articles a feed page renders before
// offering a "next" link. The store supports cursor pagination, so deep
// feeds stay responsive.
const articlesPerPage = 50

type feedView struct {
	Feed   *storage.Feed
	Unread int
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

func (s *Server) handleIndex(w http.ResponseWriter, _ *http.Request) {
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		http.Error(w, "failed to load feeds: "+err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Slice(feeds, func(i, j int) bool {
		return feeds[i].Title < feeds[j].Title
	})
	views := make([]feedView, 0, len(feeds))
	for _, f := range feeds {
		views = append(views, feedView{Feed: f, Unread: s.unreadCount(f.ID)})
	}
	s.render(w, "index.html", indexData{Feeds: views})
}

// unreadCount counts unread articles in a feed. It scans the feed's
// articles; feeds are bounded in size so this stays cheap. Errors collapse
// to 0 — an unread badge is not worth failing a page render over.
func (s *Server) unreadCount(feedID string) int {
	articles, err := s.store.GetArticles(feedID, 0)
	if err != nil {
		return 0
	}
	n := 0
	for _, a := range articles {
		if !a.Read {
			n++
		}
	}
	return n
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
