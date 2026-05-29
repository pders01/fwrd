// Package web provides a read-only HTTP front-end over the same storage,
// feed, and search backends used by the TUI and CLI. It renders article
// content as sanitized HTML — the form RSS content is authored in — rather
// than degrading it to terminal markdown the way the TUI must.
package web

import (
	"net/http"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

// Server holds the dependencies shared across handlers. It owns nothing
// the caller doesn't already own: the store and searcher are passed in
// and closed by the caller, mirroring how runTUI manages them.
type Server struct {
	store    *storage.Store
	searcher search.Searcher
	cfg      *config.Config
	tmpl     *templates
}

// NewServer wires handlers over the given backends. searcher may be nil;
// in that case the /search route reports that search is unavailable
// rather than panicking.
func NewServer(store *storage.Store, searcher search.Searcher, cfg *config.Config) (*Server, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{store: store, searcher: searcher, cfg: cfg, tmpl: tmpl}, nil
}

// Handler builds the route table. Go 1.22 ServeMux method+path patterns
// keep this dependency-free — no router library needed.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /feeds/{id}", s.handleFeed)
	// Article IDs are composite (feedID:articleURL) and contain slashes and
	// query chars, so they can't ride in a path segment — use a query param.
	mux.HandleFunc("GET /article", s.handleArticle)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /static/style.css", s.handleCSS)
	return mux
}

// ListenAndServe runs the server on addr with sane timeouts. Read/write
// timeouts guard against slow-loris-style clients even on a personal box.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return srv.ListenAndServe()
}
