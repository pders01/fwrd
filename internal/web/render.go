package web

import (
	"embed"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/pders01/fwrd/internal/storage"
)

//go:embed templates/*.html templates/style.css
var assets embed.FS

// pages are the content templates; each is parsed together with layout.html
// into its own set so their "title"/"content" block definitions don't
// collide in a shared namespace.
var pages = []string{"index.html", "feed.html", "article.html", "search.html"}

// sanitizer strips scripts, event handlers, and other active content from
// feed-supplied HTML before it is marked template.HTML and rendered. Feed
// content is untrusted, so this is the security boundary for the web view.
// UGCPolicy is the same policy the TUI uses (internal/tui/htmlmd.go).
var (
	sanitizerOnce sync.Once
	sanitizer     *bluemonday.Policy
)

func getSanitizer() *bluemonday.Policy {
	sanitizerOnce.Do(func() {
		sanitizer = bluemonday.UGCPolicy()
		// Allow images with their attributes so article media renders.
		sanitizer.AllowAttrs("src", "alt", "title").OnElements("img")
	})
	return sanitizer
}

type templates struct {
	sets map[string]*template.Template
	css  []byte
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"date": func(tm time.Time) string {
			if tm.IsZero() {
				return ""
			}
			return tm.Format("2006-01-02 15:04")
		},
	}
}

func loadTemplates() (*templates, error) {
	sets := make(map[string]*template.Template, len(pages))
	for _, page := range pages {
		t, err := template.New(page).Funcs(funcMap()).
			ParseFS(assets, "templates/layout.html", "templates/"+page)
		if err != nil {
			return nil, err
		}
		sets[page] = t
	}
	css, err := assets.ReadFile("templates/style.css")
	if err != nil {
		return nil, err
	}
	return &templates{sets: sets, css: css}, nil
}

// articleBody picks the richest available HTML for an article: full
// Content if present, otherwise the Description summary.
func articleBody(a *storage.Article) string {
	if a.Content != "" {
		return a.Content
	}
	return a.Description
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	set := s.tmpl.sets[name]
	if set == nil {
		http.Error(w, "unknown template: "+name, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := set.ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
	}
}

func (s *Server) handleCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	_, _ = w.Write(s.tmpl.css)
}
