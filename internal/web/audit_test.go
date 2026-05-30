package web

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pders01/fwrd/internal/audit"
)

func TestAuditLogMiddlewareCapturesStatusAndIP(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{audit: audit.NewWriter(&buf)}
	h := s.auditLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("hello"))
	}))

	req := httptest.NewRequest(http.MethodGet, "/feeds?q=x", http.NoBody)
	req.RemoteAddr = "10.0.0.5:54321"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	// The wrapper must not disturb the response.
	if rec.Code != http.StatusCreated || rec.Body.String() != "hello" {
		t.Fatalf("response altered: code=%d body=%q", rec.Code, rec.Body.String())
	}

	var got audit.Record
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad audit line %q: %v", buf.String(), err)
	}
	if got.Dir != audit.In {
		t.Errorf("dir = %q, want in", got.Dir)
	}
	if got.Method != "GET" || got.URL != "/feeds?q=x" {
		t.Errorf("method/url mismatch: %+v", got)
	}
	if got.Status != http.StatusCreated {
		t.Errorf("status = %d, want 201", got.Status)
	}
	if got.Bytes != 5 {
		t.Errorf("bytes = %d, want 5", got.Bytes)
	}
	if got.ClientIP != "10.0.0.5" {
		t.Errorf("client IP = %q, want 10.0.0.5 (port stripped)", got.ClientIP)
	}
}

func TestAuditLogMiddlewareDefaultsStatus200(t *testing.T) {
	var buf bytes.Buffer
	s := &Server{audit: audit.NewWriter(&buf)}
	h := s.auditLog(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok")) // no explicit WriteHeader
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", http.NoBody))

	var got audit.Record
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("bad audit line: %v", err)
	}
	if got.Status != http.StatusOK {
		t.Errorf("status = %d, want implicit 200", got.Status)
	}
}
