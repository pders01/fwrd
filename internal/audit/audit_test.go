package audit

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func TestLoggerLogWritesJSONLine(t *testing.T) {
	var buf bytes.Buffer
	l := NewWriter(&buf)
	l.Log(&Record{Dir: In, Method: "GET", URL: "/feeds", Status: 200, ClientIP: "127.0.0.1"})

	out := buf.String()
	if !strings.HasSuffix(out, "\n") {
		t.Fatalf("record not newline-terminated: %q", out)
	}
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("want exactly one line, got %q", out)
	}

	var got Record
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("logged line is not valid JSON: %v", err)
	}
	if got.Dir != In || got.Method != "GET" || got.URL != "/feeds" || got.Status != 200 {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.Time == "" {
		t.Error("Time was not stamped at write")
	}
}

func TestLoggerOmitsEmptyFields(t *testing.T) {
	var buf bytes.Buffer
	NewWriter(&buf).Log(&Record{Dir: Out, Method: "GET", URL: "https://x"})
	// Source/User/err/status etc. carry omitempty; absent fields must not appear.
	for _, k := range []string{`"source"`, `"user"`, `"err"`, `"ip"`, `"status"`} {
		if strings.Contains(buf.String(), k) {
			t.Errorf("expected %s to be omitted, got %s", k, buf.String())
		}
	}
}

func TestLoggerNilSafe(t *testing.T) {
	var l *Logger
	// Must not panic on a nil logger — callers hold a possibly-disabled one.
	l.Log(&Record{Dir: In, Method: "GET", URL: "/"})
	if err := l.Close(); err != nil {
		t.Errorf("nil Close returned %v", err)
	}
}

// stubRT is a RoundTripper that returns a canned response or error.
type stubRT struct {
	resp *http.Response
	err  error
}

func (s stubRT) RoundTrip(*http.Request) (*http.Response, error) { return s.resp, s.err }

func TestRoundTripperLogsOutbound(t *testing.T) {
	var buf bytes.Buffer
	base := stubRT{resp: &http.Response{StatusCode: 201, ContentLength: 42, Body: http.NoBody}}
	rt := &RoundTripper{Base: base, Log: NewWriter(&buf)}

	req, _ := http.NewRequest("GET", "https://example.com/feed.xml", http.NoBody)
	req = req.WithContext(WithSource(req.Context(), "feed"))
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()

	var got Record
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad audit line: %v", err)
	}
	if got.Dir != Out {
		t.Errorf("dir = %q, want out", got.Dir)
	}
	if got.Source != "feed" {
		t.Errorf("source = %q, want feed", got.Source)
	}
	if got.Status != 201 || got.Bytes != 42 || got.URL != "https://example.com/feed.xml" {
		t.Errorf("record mismatch: %+v", got)
	}
}

func TestRoundTripperLogsError(t *testing.T) {
	var buf bytes.Buffer
	base := stubRT{err: errors.New("dial timeout")}
	rt := &RoundTripper{Base: base, Log: NewWriter(&buf)}

	req, _ := http.NewRequest("GET", "https://down.example", http.NoBody)
	if _, err := rt.RoundTrip(req); err == nil { //nolint:bodyclose // nil response on error
		t.Fatal("expected the underlying error to propagate")
	}

	var got Record
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad audit line: %v", err)
	}
	if got.Err == "" || got.Status != 0 {
		t.Errorf("want err recorded and status 0, got %+v", got)
	}
}

func TestRoundTripperNilLogPassesThrough(t *testing.T) {
	called := false
	base := rtFunc(func(*http.Request) (*http.Response, error) {
		called = true
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	rt := &RoundTripper{Base: base, Log: nil}
	req, _ := http.NewRequest("GET", "https://x", http.NoBody)
	resp, err := rt.RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	resp.Body.Close()
	if !called {
		t.Error("base transport was not invoked")
	}
}

type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
