package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

type refreshResultOps struct {
	blockingOps
	calls   atomic.Int32
	refresh func(context.Context, bool) (feed.RefreshSummary, error)
}

func (o *refreshResultOps) RefreshAllFeeds(ctx context.Context, force bool) (feed.RefreshSummary, error) {
	o.calls.Add(1)
	return o.refresh(ctx, force)
}

func startRefreshRPC(t *testing.T, makeOps func(*storage.Store) FeedOperations) (*storage.Store, *RPCServer, ipc.Endpoint) {
	t.Helper()
	store, err := storage.NewLocalStoreWithTimeout(filepath.Join(t.TempDir(), "refresh.db"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ep := ipc.NewEndpoint(filepath.Join(t.TempDir(), "refresh-rpc.db"))
	server, err := NewRPCServer(ep, store, NewMutationCoordinator(makeOps(store)))
	if err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
		_ = store.Close()
	})
	return store, server, ep
}

func refreshClient(t *testing.T, ep ipc.Endpoint) *ipc.Client {
	t.Helper()
	client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func readRefreshResponse(t *testing.T, conn net.Conn, req ipc.Request) ipc.Response {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := ipc.WriteFrame(conn, req); err != nil {
		t.Fatal(err)
	}
	body, err := ipc.ReadFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var response ipc.Response
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.ID != req.ID || response.Version != ipc.ProtocolVersion {
		t.Fatalf("response envelope = %+v", response)
	}
	if response.Error != nil && len(response.Result) > 0 && string(response.Result) != "null" {
		t.Fatalf("response contains both result and error: %s", body)
	}
	return response
}

func TestRPCRefreshSummaryRoundTrip(t *testing.T) {
	partialErrors := []error{
		errors.New("fetching feed alpha: HTTP error: 504"),
		errors.New("fetching feed beta: HTTP error: 504"),
		errors.New("fetching feed gamma: HTTP error: 504"),
	}
	for _, tc := range []struct {
		name    string
		summary feed.RefreshSummary
		err     error
		code    string
	}{
		{name: "empty"},
		{name: "success", summary: feed.RefreshSummary{UpdatedFeeds: 2, AddedArticles: 7}},
		{name: "partial", summary: feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 3, Errors: partialErrors}, err: errors.Join(partialErrors...)},
		{name: "all failed", summary: feed.RefreshSummary{Errors: partialErrors}, err: errors.Join(partialErrors...)},
		{name: "per-feed deadline", summary: feed.RefreshSummary{UpdatedFeeds: 1, Errors: []error{fmt.Errorf("fetching feed slow: %w", context.DeadlineExceeded)}}, err: fmt.Errorf("fetching feed slow: %w", context.DeadlineExceeded)},
		{name: "per-feed canceled", summary: feed.RefreshSummary{Errors: []error{fmt.Errorf("fetching feed interrupted: %w", context.Canceled)}}, err: fmt.Errorf("fetching feed interrupted: %w", context.Canceled)},
		{name: "summary errors without joined error", summary: feed.RefreshSummary{Errors: partialErrors}},
		{name: "setup failure", err: errors.New("getting feeds: database unavailable"), code: ipc.CodeInternal},
		{name: "top-level failure with partial summary", summary: feed.RefreshSummary{UpdatedFeeds: 1, Errors: partialErrors}, err: errors.New("operation failed outside feed refresh"), code: ipc.CodeInternal},
		{name: "additional top-level failure", summary: feed.RefreshSummary{UpdatedFeeds: 1, Errors: partialErrors}, err: errors.Join(errors.Join(partialErrors...), errors.New("operation failed outside feed refresh")), code: ipc.CodeInternal},
		{name: "top-level deadline", err: context.DeadlineExceeded, code: ipc.CodeUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := &refreshResultOps{refresh: func(_ context.Context, force bool) (feed.RefreshSummary, error) {
				if !force {
					return feed.RefreshSummary{}, errors.New("force was not forwarded")
				}
				return tc.summary, tc.err
			}}
			_, server, ep := startRefreshRPC(t, func(*storage.Store) FeedOperations { return ops })
			conn := rawRPCConnection(t, ep)
			response := readRefreshResponse(t, conn, ipc.Request{Version: ipc.ProtocolVersion, ID: 1, Method: "RefreshAllFeeds", Params: map[string]bool{"force": true}})
			if tc.code != "" {
				if response.Error == nil || response.Error.Code != tc.code || response.Error.Message != tc.err.Error() {
					t.Fatalf("RPC error = %+v, want %s: %v", response.Error, tc.code, tc.err)
				}
			} else {
				if response.Error != nil {
					t.Fatalf("completed refresh returned RPC error: %v", response.Error)
				}
				var wire refreshSummaryWire
				if err := json.Unmarshal(response.Result, &wire); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(wire, encodeRefreshSummary(tc.summary)) {
					t.Fatalf("wire summary = %+v, want %+v", wire, encodeRefreshSummary(tc.summary))
				}
			}
			// A second call on the same raw connection must work after either
			// a partial result or a genuine RPC error.
			next := readRefreshResponse(t, conn, ipc.Request{Version: ipc.ProtocolVersion, ID: 2, Method: "GetAllFeeds"})
			if next.Error != nil {
				t.Fatalf("subsequent RPC: %v", next.Error)
			}

			client := refreshClient(t, ep)
			summary, err := newRemoteOps(client, config.TestConfig()).RefreshAllFeeds(context.Background(), true)
			if tc.code != "" {
				var rpcErr *ipc.ErrorBody
				if !errors.As(err, &rpcErr) || rpcErr.Code != tc.code {
					t.Fatalf("remote error = %v, want RPC code %s", err, tc.code)
				}
			} else {
				if !reflect.DeepEqual(encodeRefreshSummary(summary), encodeRefreshSummary(tc.summary)) {
					t.Fatalf("remote summary = %+v, want %+v", summary, tc.summary)
				}
				wantErr := errors.Join(tc.summary.Errors...)
				if (err == nil) != (wantErr == nil) || (err != nil && err.Error() != wantErr.Error()) {
					t.Fatalf("remote joined error = %v, want %v", err, wantErr)
				}
				var rpcErr *ipc.ErrorBody
				if errors.As(err, &rpcErr) {
					t.Fatalf("partial failure escaped as RPC error: %v", err)
				}
			}
			waitRPCCondition(t, func() bool { return activeRPCConnections(server) == 0 })
			server.mu.Lock()
			connections := make([]net.Conn, 0, len(server.conns))
			for conn := range server.conns {
				connections = append(connections, conn)
			}
			server.mu.Unlock()
			if callErr := client.Call("FeedStats", nil, nil); callErr != nil {
				t.Fatalf("connection reuse: %v", callErr)
			}
			server.mu.Lock()
			reused := len(server.conns) == len(connections)
			for _, conn := range connections {
				if _, ok := server.conns[conn]; !ok {
					reused = false
				}
			}
			server.mu.Unlock()
			if !reused || !client.IsConnected() {
				t.Fatal("RPC connection replaced after result")
			}
			if ops.calls.Load() != 2 {
				t.Fatalf("refresh replayed: calls=%d, want 2 explicit calls", ops.calls.Load())
			}
		})
	}
}

func TestRPCRefreshPartialSummaryPersistsSuccessfulUpdates(t *testing.T) {
	var healthyRequests, failedRequests atomic.Int32
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthy" {
			failedRequests.Add(1)
			http.Error(w, "fixture gateway timeout", http.StatusGatewayTimeout)
			return
		}
		healthyRequests.Add(1)
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Updated healthy feed</title><item><title>Persisted article</title><link>https://example.test/article</link><guid>new-article</guid></item></channel></rss>`)
	}))
	defer fixture.Close()
	cfg := config.TestConfig()
	cfg.Feed.RefreshInterval = 0
	store, _, ep := startRefreshRPC(t, func(store *storage.Store) FeedOperations {
		manager := feed.NewManager(store, cfg)
		manager.SetPermissiveValidation(true)
		return &LocalOps{Store: store, Manager: manager}
	})
	for _, id := range []string{"healthy", "failed-one", "failed-two", "failed-three"} {
		if err := store.SaveFeed(&storage.Feed{ID: id, URL: fixture.URL + "/" + id, Title: "Old " + id}); err != nil {
			t.Fatal(err)
		}
	}
	client := refreshClient(t, ep)
	summary, err := newRemoteOps(client, cfg).RefreshAllFeeds(context.Background(), true)
	if summary.UpdatedFeeds != 1 || summary.AddedArticles != 1 || len(summary.Errors) != 3 || err == nil {
		t.Fatalf("refresh = %+v, %v", summary, err)
	}
	for _, feedErr := range summary.Errors {
		if feedErr == nil || !strings.Contains(feedErr.Error(), "504") || !strings.Contains(err.Error(), feedErr.Error()) {
			t.Fatalf("lost meaningful per-feed error: %v (joined %v)", feedErr, err)
		}
	}
	if healthyRequests.Load() != 1 || failedRequests.Load() != 3 {
		t.Fatalf("refresh replayed: healthy=%d failed=%d", healthyRequests.Load(), failedRequests.Load())
	}
	remoteStore := storage.NewRPCStore(client)
	healthy, getErr := remoteStore.GetFeed("healthy")
	if getErr != nil || healthy.Title != "Updated healthy feed" || healthy.LastFetched.IsZero() {
		t.Fatalf("persisted healthy feed = %+v, %v", healthy, getErr)
	}
	articles, getErr := remoteStore.GetArticles("healthy", 10)
	if getErr != nil || len(articles) != 1 || articles[0].Title != "Persisted article" {
		t.Fatalf("persisted articles = %+v, %v", articles, getErr)
	}
	for _, id := range []string{"failed-one", "failed-two", "failed-three"} {
		failed, getErr := remoteStore.GetFeed(id)
		if getErr != nil || !strings.Contains(failed.LastError, "504") || failed.LastErrorAt.IsZero() {
			t.Fatalf("persisted failure %s = %+v, %v", id, failed, getErr)
		}
	}
}

func TestRPCRefreshDroppedResponseIsNotReplayed(t *testing.T) {
	var server *RPCServer
	ready := make(chan struct{})
	ops := &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
		<-ready
		// The operation completed, but its response never reaches the client.
		// Retrying this mutation would repeat successful work.
		server.closeAllConnections()
		return feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 2}, nil
	}}
	var ep ipc.Endpoint
	_, server, ep = startRefreshRPC(t, func(*storage.Store) FeedOperations { return ops })
	close(ready)
	client := refreshClient(t, ep)
	if _, err := newRemoteOps(client, config.TestConfig()).RefreshAllFeeds(context.Background(), true); err == nil {
		t.Fatal("dropped refresh response unexpectedly succeeded")
	}
	if err := client.Call("GetAllFeeds", nil, nil); err != nil {
		t.Fatalf("read after dropped response: %v", err)
	}
	if ops.calls.Load() != 1 {
		t.Fatalf("refresh with unknown outcome replayed: calls=%d", ops.calls.Load())
	}
}

func TestRPCRefreshActualRequestDeadlineIsRPCError(t *testing.T) {
	ops := &refreshResultOps{refresh: func(ctx context.Context, _ bool) (feed.RefreshSummary, error) {
		<-ctx.Done()
		summary := feed.RefreshSummary{UpdatedFeeds: 1, AddedArticles: 2, Errors: []error{fmt.Errorf("fetching feed slow: %w", ctx.Err())}}
		return summary, errors.Join(summary.Errors...)
	}}
	_, _, ep := startRefreshRPC(t, func(*storage.Store) FeedOperations { return ops })
	conn := rawRPCConnection(t, ep)
	response := readRefreshResponse(t, conn, ipc.Request{Version: ipc.ProtocolVersion, ID: 1, Method: "RefreshAllFeeds", Params: map[string]bool{"force": true}, Deadline: time.Now().Add(100 * time.Millisecond)})
	if response.Error == nil || response.Error.Code != ipc.CodeUnavailable || !strings.Contains(response.Error.Message, "deadline exceeded") {
		t.Fatalf("actual deadline returned summary instead of RPC error: %+v", response)
	}
	next := readRefreshResponse(t, conn, ipc.Request{Version: ipc.ProtocolVersion, ID: 2, Method: "GetAllFeeds"})
	if next.Error != nil || ops.calls.Load() != 1 {
		t.Fatalf("after deadline: response=%+v refresh calls=%d", next, ops.calls.Load())
	}
}

func TestRPCRefreshActualCallerCancellationIsNotReplayed(t *testing.T) {
	for _, kind := range []string{"cancel", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			started, canceled := make(chan struct{}), make(chan struct{})
			ops := &refreshResultOps{refresh: func(ctx context.Context, _ bool) (feed.RefreshSummary, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				return feed.RefreshSummary{UpdatedFeeds: 1, Errors: []error{ctx.Err()}}, ctx.Err()
			}}
			_, _, ep := startRefreshRPC(t, func(*storage.Store) FeedOperations { return ops })
			client := refreshClient(t, ep)
			ctx, cancel := context.WithCancel(context.Background())
			wantErr := context.Canceled
			if kind == "deadline" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 200*time.Millisecond)
				wantErr = context.DeadlineExceeded
			}
			defer cancel()
			done := make(chan error, 1)
			go func() {
				summary, err := newRemoteOps(client, config.TestConfig()).RefreshAllFeeds(ctx, true)
				if summary.UpdatedFeeds != 0 || len(summary.Errors) != 0 {
					done <- fmt.Errorf("canceled operation returned summary: %+v", summary)
					return
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("refresh did not start")
			}
			if kind == "cancel" {
				cancel()
			}
			if err := waitSessionCall(t, done); !errors.Is(err, wantErr) {
				t.Fatalf("caller cancellation error = %v, want %v", err, wantErr)
			}
			select {
			case <-canceled:
			case <-time.After(3 * time.Second):
				t.Fatal("server operation did not observe cancellation")
			}
			if err := client.Call("GetAllFeeds", nil, nil); err != nil {
				t.Fatalf("read after canceled mutation: %v", err)
			}
			if ops.calls.Load() != 1 {
				t.Fatalf("canceled refresh replayed: calls=%d", ops.calls.Load())
			}
		})
	}
}
