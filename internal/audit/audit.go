// Package audit provides an append-only, JSON-lines audit log of every HTTP
// request that passes through fwrd — both inbound requests served by
// `fwrd serve` and outbound requests fwrd makes when fetching feeds or running
// Lua plugins. It is off by default; the serve command enables it via
// `--audit` or the `[web.audit]` config section.
//
// Records are written one JSON object per line so the log is greppable and
// streamable with standard tools (jq, tail -f). A single Logger is shared by
// the web middleware and the outbound RoundTripper; all writes are serialized
// because net/http serves each request on its own goroutine and feed refreshes
// run concurrently.
package audit

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

// Direction marks whether a record describes a request fwrd received ("in") or
// one it made ("out").
type Direction string

const (
	In  Direction = "in"
	Out Direction = "out"
)

// Record is one audited request. Fields irrelevant to a direction are omitted
// (inbound has no Source; outbound has no ClientIP/User/Host/TLS).
type Record struct {
	Time       string    `json:"time"`             // RFC3339 (UTC); stamped at write if unset
	Dir        Direction `json:"dir"`              // "in" | "out"
	Method     string    `json:"method"`           // HTTP method
	URL        string    `json:"url"`              // inbound: request URI; outbound: full URL
	Status     int       `json:"status,omitempty"` // response status (0 if the request errored)
	DurationMS int64     `json:"dur_ms"`           // wall time the request took
	Bytes      int64     `json:"bytes,omitempty"`  // response body bytes (best effort)
	ClientIP   string    `json:"ip,omitempty"`     // inbound: remote address, port stripped
	Host       string    `json:"host,omitempty"`   // inbound: request Host header
	TLS        bool      `json:"tls,omitempty"`    // inbound: served over HTTPS
	User       string    `json:"user,omitempty"`   // inbound: Basic-Auth username (never the password)
	Source     string    `json:"source,omitempty"` // outbound: "feed" | "plugin" | ""
	Err        string    `json:"err,omitempty"`    // transport error, if any
}

// Logger appends Records to a sink as JSON lines. The zero value is not
// usable; obtain one from Open or NewWriter. A nil *Logger is a safe no-op, so
// callers can hold a possibly-disabled logger without nil-checking each call.
type Logger struct {
	mu     sync.Mutex
	w      io.Writer
	closer io.Closer
}

// Open creates (or appends to) the audit log at path. The file is opened in
// append mode so concurrent writers and restarts never truncate history.
func Open(path string) (*Logger, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	return &Logger{w: f, closer: f}, nil
}

// NewWriter returns a Logger that writes to w. Used in tests and when the sink
// is not a file.
func NewWriter(w io.Writer) *Logger { return &Logger{w: w} }

// Log appends r as one JSON line. It is safe for concurrent use and a no-op on
// a nil Logger. A marshal/write error is dropped: an audit sink must never
// break the request it is recording. r is taken by pointer to avoid copying
// the record; Log stamps r.Time if unset but reads nothing else back.
func (l *Logger) Log(r *Record) {
	if l == nil {
		return
	}
	if r.Time == "" {
		r.Time = time.Now().UTC().Format(time.RFC3339Nano)
	}
	line, err := json.Marshal(r)
	if err != nil {
		return
	}
	line = append(line, '\n')
	l.mu.Lock()
	_, _ = l.w.Write(line)
	l.mu.Unlock()
}

// Close releases the underlying sink if it owns one (a file from Open). It is a
// no-op on a nil Logger or a writer-backed Logger.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// sourceKey tags an outbound request's context with its origin so the
// RoundTripper can attribute it ("feed" vs "plugin") without inspecting URLs.
type sourceKey struct{}

// WithSource returns a context carrying src, to be read back by the audit
// RoundTripper. Call sites set it on the request before it is sent.
func WithSource(ctx context.Context, src string) context.Context {
	return context.WithValue(ctx, sourceKey{}, src)
}

func sourceFromContext(ctx context.Context) string {
	if s, ok := ctx.Value(sourceKey{}).(string); ok {
		return s
	}
	return ""
}

// RoundTripper wraps Base and records every outbound request to Log. It is
// installed on the shared feed/plugin HTTP client's Transport, so a single
// instance covers both feed fetches and plugin http.get calls. A nil Log
// passes through untouched.
type RoundTripper struct {
	Base http.RoundTripper
	Log  *Logger
}

// RoundTrip implements http.RoundTripper. It does not modify the request; it
// only times the round trip and logs the outcome.
func (rt *RoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := rt.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if rt.Log == nil {
		return base.RoundTrip(req)
	}
	start := time.Now()
	resp, err := base.RoundTrip(req)
	rec := Record{
		Dir:        Out,
		Method:     req.Method,
		URL:        req.URL.String(),
		DurationMS: time.Since(start).Milliseconds(),
		Source:     sourceFromContext(req.Context()),
	}
	if err != nil {
		rec.Err = err.Error()
	} else {
		rec.Status = resp.StatusCode
		rec.Bytes = resp.ContentLength
	}
	rt.Log.Log(&rec)
	return resp, err
}
