package plugins

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRegistryRoutingDoesNotHoldLock(t *testing.T) {
	reg := NewRegistry(time.Second)
	p := &mockPlugin{name: "reentrant", priority: 1}
	p.canHandle = func(string) bool {
		reg.Register(&mockPlugin{name: "registered-from-routing"})
		reg.Unregister("registered-from-routing")
		return true
	}
	reg.Register(p)
	done := make(chan Plugin, 1)
	go func() { done <- reg.FindPlugin("https://feed.example.org") }()
	select {
	case got := <-done:
		if got != p {
			t.Fatal("wrong plugin selected")
		}
	case <-time.After(time.Second):
		t.Fatal("plugin code executed under registry lock")
	}
}

func TestRegistryCanceledRoutingIsNotAdmitted(t *testing.T) {
	reg := NewRegistry(time.Second)
	reg.Register(&mockPlugin{name: "never", canHandle: func(string) bool {
		t.Fatal("canceled routing called plugin")
		return true
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reg.EnhanceFeed(ctx, "https://feed.example.org"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled routing = %v", err)
	}
}

func TestRegistryHTTPClientIsStrict(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	reg := NewRegistry(time.Second)
	reg.Register(&mockPlugin{name: "request", priority: 1, canHandle: func(string) bool { return true },
		enhance: func(ctx context.Context, _ string, client *http.Client) (*FeedInfo, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
			if err != nil {
				return nil, err
			}
			resp, err := client.Do(req)
			if resp != nil {
				resp.Body.Close()
			}
			return nil, err
		}})
	if _, err := reg.EnhanceFeed(context.Background(), "https://feed.example.org"); err == nil {
		t.Fatal("registry gave native plugins an insecure HTTP client")
	}
	if calls.Load() != 0 {
		t.Fatal("plugin reached a loopback server")
	}
}
