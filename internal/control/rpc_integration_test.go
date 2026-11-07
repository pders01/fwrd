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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

type recordingDataListener struct {
	mu       sync.Mutex
	feeds    int
	articles int
}

func (l *recordingDataListener) OnDataUpdated(value *storage.Feed, articles []*storage.Article) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if value != nil {
		l.feeds++
	}
	l.articles += len(articles)
}

func startTestRPC(t testing.TB, dbPath string) (*storage.Store, *RPCServer, ipc.Endpoint, *recordingDataListener) {
	t.Helper()
	store, err := storage.NewLocalStoreWithTimeout(dbPath, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.TestConfig()
	manager := feed.NewManager(store, cfg)
	listener := &recordingDataListener{}
	manager.RegisterDataListener(listener)
	coord := NewMutationCoordinator(&LocalOps{Store: store, Manager: manager})
	canonical, err := ipc.CanonicalDBPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ipc.NewEndpoint(canonical)
	server, err := NewRPCServer(endpoint, store, coord)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		if err := <-serveDone; err != nil {
			t.Errorf("Serve: %v", err)
		}
		_ = store.Close()
	})
	return store, server, endpoint, listener
}

func TestRPCBackendContractAndNotifications(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "contract.db")
	serverStore, _, endpoint, listener := startTestRPC(t, dbPath)
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	remote := storage.NewRPCStore(client)
	defer remote.Close()

	value := &storage.Feed{ID: "feed-1", URL: "https://example.com/feed", Title: "Example"}
	if saveErr := remote.SaveFeed(value); saveErr != nil {
		t.Fatal(saveErr)
	}
	articles := []*storage.Article{{ID: "article-1", FeedID: value.ID, Title: "One", Published: time.Now()}}
	if saveErr := remote.SaveArticles(articles); saveErr != nil {
		t.Fatal(saveErr)
	}

	got, err := remote.GetFeed(value.ID)
	if err != nil || got.Title != value.Title {
		t.Fatalf("GetFeed = %+v, %v", got, err)
	}
	gotArticles, err := remote.GetArticles(value.ID, 10)
	if err != nil || len(gotArticles) != 1 || gotArticles[0].ID != articles[0].ID {
		t.Fatalf("GetArticles = %+v, %v", gotArticles, err)
	}
	if markErr := remote.MarkArticleRead(articles[0].ID, true); markErr != nil {
		t.Fatal(markErr)
	}
	stats, err := remote.FeedStats()
	if err != nil {
		t.Fatal(err)
	}
	if stats[value.ID].Unread != 0 || stats[value.ID].Total != 1 {
		t.Fatalf("FeedStats = %+v", stats[value.ID])
	}
	if _, err := remote.GetFeed("missing"); !errors.Is(err, ipc.ErrNotFound) {
		t.Fatalf("GetFeed missing error = %v", err)
	}

	listener.mu.Lock()
	feedUpdates, articleUpdates := listener.feeds, listener.articles
	listener.mu.Unlock()
	if feedUpdates != 1 || articleUpdates != 1 {
		t.Fatalf("listener updates = feeds:%d articles:%d", feedUpdates, articleUpdates)
	}
	if serverStore.WriteGen() != 3 {
		t.Fatalf("server WriteGen = %d, want 3", serverStore.WriteGen())
	}
	if remote.WriteGen() != 3 {
		t.Fatalf("remote WriteGen = %d, want 3", remote.WriteGen())
	}
}

func TestRPCBackendPaginatesLargeArticleReads(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "pagination.db")
	_, _, endpoint, _ := startTestRPC(t, dbPath)
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	remote := storage.NewRPCStore(client)
	defer remote.Close()

	feedValue := &storage.Feed{ID: "feed", URL: "https://example.com/feed"}
	if saveErr := remote.SaveFeed(feedValue); saveErr != nil {
		t.Fatal(saveErr)
	}
	articles := make([]*storage.Article, 1005)
	for i := range articles {
		articles[i] = &storage.Article{
			ID:        fmt.Sprintf("article-%04d", i),
			FeedID:    feedValue.ID,
			Published: time.Unix(int64(i), 0),
		}
	}
	if saveErr := remote.SaveArticles(articles); saveErr != nil {
		t.Fatal(saveErr)
	}
	got, err := remote.GetArticles(feedValue.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(articles) {
		t.Fatalf("GetArticles returned %d articles, want %d", len(got), len(articles))
	}
}

func TestRemoteFeedLifecycleUpdatesSearch(t *testing.T) {
	var version atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		title := "firstunique"
		if version.Load() > 0 {
			title = "secondunique"
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = fmt.Fprintf(w, `<?xml version="1.0"?><rss version="2.0"><channel><title>Lifecycle</title><item><title>%s</title><link>https://example.com/item</link><guid>item-1</guid></item></channel></rss>`, title)
	}))
	defer backend.Close()

	dbPath := filepath.Join(t.TempDir(), "lifecycle.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cfg := config.TestConfig()
	cfg.Database.Path = dbPath
	cfg.Database.SearchIndex = filepath.Join(t.TempDir(), "lifecycle.bleve")
	cfg.Feed.RefreshInterval = 0
	manager := feed.NewManager(store, cfg)
	manager.SetPermissiveValidation(true)
	searcher, err := search.NewBleveEngine(store, cfg.Database.SearchIndex)
	if err != nil {
		t.Fatal(err)
	}
	defer searcher.(interface{ Close() error }).Close()
	manager.RegisterDataListener(searcher.(feed.DataListener))
	manager.RegisterBatchScope(searcher.(feed.BatchScope))
	manager.RegisterDeleteListener(searcher.(feed.DeleteListener))

	canonical, _ := ipc.CanonicalDBPath(dbPath)
	endpoint := ipc.NewEndpoint(canonical)
	rpcServer, err := NewRPCServer(endpoint, store, NewMutationCoordinator(&LocalOps{Store: store, Manager: manager}))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- rpcServer.Serve() }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = rpcServer.Shutdown(ctx)
		<-serveDone
	}()

	session, err := openRemote(endpoint, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	value, err := session.Feeds.AddFeed(context.Background(), backend.URL)
	if err != nil {
		t.Fatal(err)
	}
	results, err := searcher.Search("firstunique", 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("search after add = %#v, %v", results, err)
	}

	version.Store(1)
	if refreshErr := session.Feeds.RefreshFeed(context.Background(), value.ID, true); refreshErr != nil {
		t.Fatal(refreshErr)
	}
	results, err = searcher.Search("secondunique", 10)
	if err != nil || len(results) == 0 {
		t.Fatalf("search after refresh = %#v, %v", results, err)
	}

	if deleteErr := session.Feeds.DeleteFeed(context.Background(), value.ID); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	results, err = searcher.Search("secondunique", 10)
	if err != nil || len(results) != 0 {
		t.Fatalf("search after delete = %#v, %v", results, err)
	}
}

type blockingOps struct {
	started chan struct{}
	release chan struct{}
}

func (o *blockingOps) AddFeed(ctx context.Context, _ string) (*storage.Feed, error) {
	close(o.started)
	select {
	case <-o.release:
		return &storage.Feed{ID: "done"}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func (*blockingOps) RefreshFeed(context.Context, string, bool) error { return nil }
func (*blockingOps) RefreshAllFeeds(context.Context, bool) (feed.RefreshSummary, error) {
	return feed.RefreshSummary{}, nil
}
func (*blockingOps) DeleteFeed(context.Context, string) error { return nil }
func (*blockingOps) ImportFeeds(context.Context, []string) (ImportSummary, error) {
	return ImportSummary{}, nil
}

func TestRPCShutdownDrainsInflightOperation(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shutdown.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	canonical, _ := ipc.CanonicalDBPath(dbPath)
	endpoint := ipc.NewEndpoint(canonical)
	ops := &blockingOps{started: make(chan struct{}), release: make(chan struct{})}
	server, err := NewRPCServer(endpoint, store, NewMutationCoordinator(ops))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	callDone := make(chan error, 1)
	go func() { callDone <- client.Call("AddFeed", map[string]string{"url": "x"}, nil) }()
	<-ops.started

	shutdownDone := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { shutdownDone <- server.Shutdown(ctx) }()
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before operation drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(ops.release)
	if err := <-callDone; err != nil {
		t.Fatalf("in-flight call: %v", err)
	}
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestRPCShutdownCancelsAtDeadlineAndRejectsQueuedWork(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "shutdown-deadline.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	canonical, _ := ipc.CanonicalDBPath(dbPath)
	endpoint := ipc.NewEndpoint(canonical)
	ops := &blockingOps{started: make(chan struct{}), release: make(chan struct{})}
	server, err := NewRPCServer(endpoint, store, NewMutationCoordinator(ops))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()

	activeClient, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer activeClient.Close()
	queuedClient, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer queuedClient.Close()

	activeDone := make(chan error, 1)
	go func() { activeDone <- activeClient.Call("AddFeed", map[string]string{"url": "active"}, nil) }()
	<-ops.started
	queuedDone := make(chan error, 1)
	go func() { queuedDone <- queuedClient.Call("AddFeed", map[string]string{"url": "queued"}, nil) }()
	time.Sleep(10 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	shutdownErr := server.Shutdown(ctx)
	if !errors.Is(shutdownErr, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", shutdownErr)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("Shutdown exceeded bounded grace: %v", elapsed)
	}
	if callErr := <-queuedDone; callErr == nil {
		t.Fatal("queued operation was accepted during shutdown")
	}
	if callErr := <-activeDone; callErr == nil {
		t.Fatal("active operation was not cancelled at the deadline")
	}
	if err := <-serveDone; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestRPCRejectsMalformedParamsWithoutStoppingServer(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "malformed.db")
	_, _, endpoint, _ := startTestRPC(t, dbPath)
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	for _, test := range []struct {
		method string
		params any
	}{
		{method: "SaveArticles", params: []*storage.Article{nil}},
		{method: "MergeArticles", params: []*storage.Article{nil}},
		{method: "SaveFeed", params: nil},
		{method: "GetFeed", params: map[string]string{"id": ""}},
		{method: "GetArticlesWithCursor", params: map[string]any{"limit": 0}},
	} {
		if callErr := client.Call(test.method, test.params, nil); !errors.Is(callErr, ipc.ErrInvalidArgument) {
			t.Errorf("%s error = %v, want invalid argument", test.method, callErr)
		}
		var stats map[string]storage.FeedStat
		if callErr := client.Call("FeedStats", nil, &stats); callErr != nil {
			t.Fatalf("server stopped after malformed %s: %v", test.method, callErr)
		}
	}
}

type panicOps struct{ LocalOps }

func (*panicOps) AddFeed(context.Context, string) (*storage.Feed, error) {
	panic("malformed operation panic")
}

func TestRPCContainsDispatchPanics(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "panic.db")
	store, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager := feed.NewManager(store, config.TestConfig())
	canonical, _ := ipc.CanonicalDBPath(dbPath)
	endpoint := ipc.NewEndpoint(canonical)
	ops := &panicOps{LocalOps: LocalOps{Store: store, Manager: manager}}
	server, err := NewRPCServer(endpoint, store, NewMutationCoordinator(ops))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	defer func() {
		_ = server.Close()
		<-serveDone
	}()

	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if callErr := client.Call("AddFeed", map[string]string{"url": "https://example.com"}, nil); ipc.ErrorCode(callErr) != ipc.CodeInternal {
		t.Fatalf("panic error = %v, want internal", callErr)
	}
	if callErr := client.Call("FeedStats", nil, &map[string]storage.FeedStat{}); callErr != nil {
		t.Fatalf("server stopped after dispatch panic: %v", callErr)
	}
}

func TestWriteResultReturnsBoundedOversizeError(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	server := &RPCServer{}
	writeDone := make(chan struct{})
	go func() {
		server.writeResult(serverConn, 7, strings.Repeat("x", ipc.MaxFrameSize))
		_ = serverConn.Close()
		close(writeDone)
	}()
	body, err := ipc.ReadFrame(clientConn)
	if err != nil {
		t.Fatal(err)
	}
	var response ipc.Response
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	if response.Error == nil || response.Error.Code != ipc.CodeResultTooLarge {
		t.Fatalf("oversize response error = %#v", response.Error)
	}
	<-writeDone
}

func TestOpenSessionUsesIPCWhenDatabaseIsLocked(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "locked.db")
	_, _, _, _ = startTestRPC(t, dbPath)

	cfg := config.TestConfig()
	cfg.Database.Path = dbPath
	session, err := OpenSession(dbPath, cfg, WithOpenTimeout(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !session.IsRemote() {
		t.Fatal("OpenSession returned a local session while serve owns the database")
	}
}

func TestOpenSessionIsolatesCustomDatabases(t *testing.T) {
	serverPath := filepath.Join(t.TempDir(), "server.db")
	serverStore, _, _, _ := startTestRPC(t, serverPath)
	otherPath := filepath.Join(t.TempDir(), "other.db")
	cfg := config.TestConfig()
	cfg.Database.Path = otherPath
	session, err := OpenSession(otherPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.IsRemote() {
		t.Fatal("session for a different database connected to the server")
	}
	if err := session.Store.SaveFeed(&storage.Feed{ID: "other"}); err != nil {
		t.Fatal(err)
	}
	if _, err := serverStore.GetFeed("other"); err == nil {
		t.Fatal("write to custom database appeared in server database")
	}
}

func TestHandshakeRejectsWrongDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "identity.db")
	_, _, endpoint, _ := startTestRPC(t, dbPath)
	_, err := ipc.NewClient(endpoint.Address, "wrong-database-id")
	if !errors.Is(err, ipc.ErrDatabaseMismatch) {
		t.Fatalf("NewClient error = %v", err)
	}
}

func TestOpenSessionPreservesMemoryPath(t *testing.T) {
	cfg := config.TestConfig()
	session, err := OpenSession(storage.MemoryPath, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.IsRemote() {
		t.Fatal("MemoryPath session must be local")
	}
	if err := session.Store.SaveFeed(&storage.Feed{ID: "memory"}); err != nil {
		t.Fatal(err)
	}
}
