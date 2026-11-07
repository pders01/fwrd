package feed

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pders01/fwrd/internal/audit"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func TestFetcherStrictByDefaultAndSharedPolicy(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/rss", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	fetcher := NewFetcher(config.TestConfig())
	var log bytes.Buffer
	fetcher.client.Transport = &audit.RoundTripper{Base: fetcher.client.Transport, Log: audit.NewWriter(&log)}
	shared := fetcher.client // also handed to host plugin bindings
	for _, permissive := range []bool{false, true, false} {
		fetcher.SetPermissiveValidation(permissive)
		resp, _, err := fetcher.Fetch(&storage.Feed{URL: server.URL + "/redirect?token=secret"})
		if resp != nil {
			resp.Body.Close()
		}
		if (err == nil) != permissive {
			t.Fatalf("permissive=%t err=%v", permissive, err)
		}
		if fetcher.client != shared {
			t.Fatal("configuration replaced the shared plugin client")
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("got %d requests, want permissive request and redirect only", requests.Load())
	}
	if strings.Contains(log.String(), "secret") {
		t.Fatal("audit leaked a query credential")
	}
}

func TestFetcherHTTPErrorDoesNotExposeURLSecrets(t *testing.T) {
	fetcher := NewFetcher(config.TestConfig())
	resp, _, err := fetcher.Fetch(&storage.Feed{URL: "http://username:password@127.0.0.1/?token=secret"})
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("expected blocked request")
	}
	for _, secret := range []string{"username", "password", "secret", "token"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("HTTP error leaked %q: %v", secret, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	resp, _, err = fetcher.FetchContext(ctx, &storage.Feed{URL: "https://feed.example.org/"})
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation identity lost: %v", err)
	}
}
