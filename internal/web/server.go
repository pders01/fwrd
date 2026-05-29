// Package web provides an HTTP front-end over the same storage, feed, and
// search backends used by the TUI and CLI. It renders article content as
// sanitized HTML — the form RSS content is authored in — rather than
// degrading it to terminal markdown the way the TUI must. State-changing
// actions (add/delete/refresh feeds, mark read) are exposed as no-JS POST
// forms guarded by a same-origin check.
package web

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

// shutdownGrace bounds how long a graceful shutdown waits for in-flight
// requests (e.g. a synchronous feed refresh) to finish before connections
// are forced closed.
const shutdownGrace = 15 * time.Second

// Server holds the dependencies shared across handlers. It owns nothing
// the caller doesn't already own: the store, manager, and searcher are
// passed in and closed by the caller, mirroring how runTUI manages them.
type Server struct {
	store       *storage.Store
	manager     *feed.Manager
	searcher    search.Searcher
	cfg         *config.Config
	tmpl        *templates
	readingFont string // resolved CSS font-family for reading text
	authUser    string // HTTP Basic Auth username; empty disables auth
	authPass    string // HTTP Basic Auth password

	// writeMu serializes operations that notify the search index. The
	// DataListener/DeleteListener contract requires that notifications not
	// arrive concurrently; net/http runs each request in its own goroutine,
	// so without this two mutating requests could race the bleve batch.
	writeMu sync.Mutex
}

// NewServer wires handlers over the given backends. manager drives feed
// add/refresh and may be nil to disable those routes (they 503 instead).
// searcher may be nil; the /search route then reports search unavailable.
func NewServer(store *storage.Store, manager *feed.Manager, searcher search.Searcher, cfg *config.Config) (*Server, error) {
	tmpl, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	font := ""
	authUser, authPass := "", ""
	if cfg != nil {
		font = cfg.Web.Font
		authUser = cfg.Web.Auth.Username
		authPass = cfg.Web.Auth.Password
	}
	return &Server{
		store:       store,
		manager:     manager,
		searcher:    searcher,
		cfg:         cfg,
		tmpl:        tmpl,
		readingFont: resolveFont(font),
		authUser:    authUser,
		authPass:    authPass,
	}, nil
}

// AuthEnabled reports whether HTTP Basic Auth is configured. The serve
// command uses it to decide whether to warn about an unauthenticated
// non-loopback bind.
func (s *Server) AuthEnabled() bool { return s.authUser != "" }

// Handler builds the route table. Go 1.22 ServeMux method+path patterns
// keep this dependency-free — no router library needed. Mutating routes
// are POST-only and run behind the same-origin guard.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleFront)
	mux.HandleFunc("GET /feeds", s.handleFeeds)
	mux.HandleFunc("GET /topic/{slug}", s.handleTopic)
	mux.HandleFunc("GET /feeds/{id}", s.handleFeed)
	// Article IDs are composite (feedID:articleURL) and contain slashes and
	// query chars, so they can't ride in a path segment — use a query param.
	mux.HandleFunc("GET /article", s.handleArticle)
	mux.HandleFunc("GET /search", s.handleSearch)
	mux.HandleFunc("GET /static/style.css", s.handleCSS)
	mux.HandleFunc("GET /static/app.js", s.handleJS)
	mux.HandleFunc("GET /favicon.svg", s.handleFavicon)
	mux.HandleFunc("GET /favicon.ico", s.handleFavicon)
	mux.HandleFunc("GET /opml/export", s.handleOPMLExport)

	mux.HandleFunc("POST /feeds", s.handleAddFeed)
	mux.HandleFunc("POST /opml/import", s.handleOPMLImport)
	mux.HandleFunc("POST /feeds/{id}/refresh", s.handleRefreshFeed)
	mux.HandleFunc("POST /feeds/{id}/delete", s.handleDeleteFeed)
	mux.HandleFunc("POST /refresh", s.handleRefreshAll)
	mux.HandleFunc("POST /read", s.handleToggleRead)
	mux.HandleFunc("POST /star", s.handleToggleStar)

	// basicAuth is outermost so unauthenticated requests never reach a
	// handler; the same-origin guard then gates the mutating routes.
	return s.basicAuth(s.sameOriginGuard(mux))
}

// basicAuth gates every request behind HTTP Basic Auth when credentials
// are configured. With no username set it is a pass-through, preserving
// the open localhost default. Credential comparison is constant-time to
// avoid leaking length/equality through timing.
func (s *Server) basicAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.authUser == "" {
			next.ServeHTTP(w, r)
			return
		}
		user, pass, ok := r.BasicAuth()
		userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.authUser)) == 1
		passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.authPass)) == 1
		if !ok || !userOK || !passOK {
			w.Header().Set("WWW-Authenticate", `Basic realm="fwrd", charset="UTF-8"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOriginGuard rejects state-changing requests whose Origin/Referer host
// does not match the request host. GET/HEAD are read-only and pass through.
// This blocks cross-site form POSTs (CSRF) against the local server.
func (s *Server) sameOriginGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r) {
			http.Error(w, "cross-origin request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sameOrigin reports whether the request's Origin (or, failing that,
// Referer) host matches the Host it was sent to. A request with neither
// header is treated as same-origin: curl and other non-browser clients
// omit them, and they cannot be the vehicle for a browser-driven CSRF.
func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		origin = r.Header.Get("Referer")
	}
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return u.Host == r.Host
}

// ListenAndServe runs the server on addr with sane timeouts. Read/write
// timeouts guard against slow-loris-style clients even on a personal box.
// WriteTimeout is generous because a synchronous feed refresh makes
// network calls within the request.
//
// On SIGINT/SIGTERM it shuts down gracefully: it stops accepting new
// connections and drains in-flight requests for up to shutdownGrace before
// returning, so the caller's deferred store/index Close runs and releases
// the BoltDB and Bleve locks cleanly instead of relying on the OS to reap
// them. Returns nil on a clean shutdown.
func (s *Server) ListenAndServe(addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		// Failed to bind, or otherwise stopped without a signal.
		return err
	case <-ctx.Done():
		stop() // restore default signal handling so a second ^C kills hard
		fmt.Fprintln(os.Stderr, "\nshutting down… (waiting for in-flight requests)")
		shutCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		if err := srv.Shutdown(shutCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		// Drain the goroutine's result; ListenAndServe returns
		// ErrServerClosed once Shutdown completes.
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
