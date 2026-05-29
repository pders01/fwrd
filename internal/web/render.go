package web

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"sync"
	"time"

	"github.com/microcosm-cc/bluemonday"
	"github.com/pders01/fwrd/internal/storage"
)

//go:embed templates/*.html templates/style.css templates/app.js
var assets embed.FS

// pages are the content templates; each is parsed together with layout.html
// into its own set so their "title"/"content" block definitions don't
// collide in a shared namespace.
var pages = []string{"front.html", "topic.html", "feeds.html", "feed.html", "article.html", "search.html"}

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
	js   []byte
}

func funcMap() template.FuncMap {
	return template.FuncMap{
		"date": func(tm time.Time) string {
			if tm.IsZero() {
				return ""
			}
			return tm.Format("2006-01-02 15:04")
		},
		"longdate": func(tm time.Time) string {
			if tm.IsZero() {
				return ""
			}
			return tm.Format("Monday, January 2, 2006")
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
	js, err := assets.ReadFile("templates/app.js")
	if err != nil {
		return nil, err
	}
	return &templates{sets: sets, css: css, js: js}, nil
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
	// Render into a buffer first: ExecuteTemplate writing straight to w
	// would commit a 200 and partial body before a mid-stream error, after
	// which http.Error can no longer set a 500 — the client gets truncated
	// HTML. Buffering lets a failed render surface as a clean 500.
	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, "layout", data); err != nil {
		http.Error(w, "render error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = buf.WriteTo(w)
}

func (s *Server) handleCSS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	// Inject the configured reading font as a CSS variable ahead of the
	// static stylesheet, which references var(--reading-font) with its own
	// fallback. UI chrome keeps a fixed system sans stack.
	if s.readingFont != "" {
		_, _ = w.Write([]byte(":root{--reading-font:" + s.readingFont + ";--ui-font:" + systemSans + "}\n"))
	}
	_, _ = w.Write(s.tmpl.css)
}

func (s *Server) handleJS(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	_, _ = w.Write(s.tmpl.js)
}
