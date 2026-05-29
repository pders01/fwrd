package web

import (
	"html/template"
	"net/http"
	"sort"

	"github.com/pders01/fwrd/internal/storage"
)

// articlesPerFeed bounds how many articles a feed page renders. The store
// supports cursor pagination; the web viewer keeps it simple with a cap.
const articlesPerFeed = 100

type indexData struct {
	Feeds []*storage.Feed
}

type feedData struct {
	Feed     *storage.Feed
	Articles []*storage.Article
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
	s.render(w, "index.html", indexData{Feeds: feeds})
}

func (s *Server) handleFeed(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	feed, err := s.store.GetFeed(id)
	if err != nil || feed == nil {
		http.NotFound(w, r)
		return
	}
	articles, err := s.store.GetArticles(id, articlesPerFeed)
	if err != nil {
		http.Error(w, "failed to load articles: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.render(w, "feed.html", feedData{Feed: feed, Articles: articles})
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
		results, err := s.searcher.Search(q, articlesPerFeed)
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
