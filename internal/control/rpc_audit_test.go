package control

import (
	"context"
	"encoding/binary"
	"errors"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

type cancellationOps struct {
	blockingOps
	calls     atomic.Int32
	cancelled chan struct{}
}

func (o *cancellationOps) AddFeed(ctx context.Context, _ string) (*storage.Feed, error) {
	if o.calls.Add(1) == 1 {
		close(o.started)
	}
	select {
	case <-ctx.Done():
		close(o.cancelled)
		return nil, ctx.Err()
	case <-o.release:
		return &storage.Feed{ID: "done"}, nil
	}
}

func startCancellationRPC(t *testing.T, frameBytes ...int64) (*RPCServer, ipc.Endpoint, *cancellationOps) {
	t.Helper()
	s, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	ep := ipc.NewEndpoint(filepath.Join(t.TempDir(), "cancellation.db"))
	ops := &cancellationOps{blockingOps: blockingOps{started: make(chan struct{}), release: make(chan struct{})}, cancelled: make(chan struct{})}
	server, err := NewRPCServer(ep, s, NewMutationCoordinator(ops))
	if err != nil {
		t.Fatal(err)
	}
	if len(frameBytes) > 0 {
		server.frameBudget = ipc.NewFrameBudget(frameBytes[0])
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve() }()
	t.Cleanup(func() { _ = server.Close(); <-done; _ = s.Close() })
	return server, ep, ops
}

func waitRPCCondition(t *testing.T, condition func() bool) {
	t.Helper()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for !condition() {
		select {
		case <-ticker.C:
		case <-timer.C:
			t.Fatal("RPC condition did not become true")
		}
	}
}

func activeRPCConnections(server *RPCServer) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	n := 0
	for _, active := range server.conns {
		if active {
			n++
		}
	}
	return n
}

func TestRPCCancelledQueuedMutationIsNeverExecuted(t *testing.T) {
	server, ep, ops := startCancellationRPC(t)
	first, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	firstDone := make(chan error, 1)
	go func() { firstDone <- first.Call("AddFeed", map[string]string{"url": "first"}, nil) }()
	<-ops.started
	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.CallWithContext(ctx, "AddFeed", map[string]string{"url": "second"}, nil, time.Second)
	}()
	waitRPCCondition(t, func() bool { return activeRPCConnections(server) == 2 })
	cancel()
	if callErr := waitSessionCall(t, secondDone); !errors.Is(callErr, context.Canceled) {
		t.Fatalf("queued cancellation = %v", callErr)
	}
	// Keep the coordinator occupied until the server has observed disconnect
	// and rejected the queued request, rather than relying on timer sleeps.
	waitRPCCondition(t, func() bool { return activeRPCConnections(server) == 1 })
	close(ops.release)
	if callErr := waitSessionCall(t, firstDone); callErr != nil {
		t.Fatal(callErr)
	}
	if ops.calls.Load() != 1 {
		t.Fatalf("cancelled queued mutation ran: calls=%d", ops.calls.Load())
	}
}

func TestRPCDisconnectAndDeadlineCancelRunningOperation(t *testing.T) {
	for _, interrupt := range []string{"disconnect", "deadline"} {
		t.Run(interrupt, func(t *testing.T) {
			_, ep, ops := startCancellationRPC(t)
			client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			timeout := time.Second
			if interrupt == "deadline" {
				timeout = 50 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { done <- client.CallWithTimeout("AddFeed", map[string]string{"url": "x"}, nil, timeout) }()
			<-ops.started
			if interrupt == "disconnect" {
				_ = client.Close()
			}
			select {
			case <-ops.cancelled:
			case <-time.After(time.Second):
				t.Fatal("running operation did not receive cancellation")
			}
			if callErr := waitSessionCall(t, done); callErr == nil {
				t.Fatal("interrupted mutation succeeded")
			}
			if ops.calls.Load() != 1 {
				t.Fatalf("mutation replayed: %d calls", ops.calls.Load())
			}
		})
	}
}

func rawRPCConnection(t *testing.T, ep ipc.Endpoint) net.Conn {
	t.Helper()
	conn, err := ipc.DialEndpoint(ep.Address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if writeErr := ipc.WriteFrame(conn, ipc.Handshake{Version: ipc.ProtocolVersion, DatabaseID: ep.DatabaseID}); writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, readErr := ipc.ReadFrame(conn); readErr != nil {
		t.Fatal(readErr)
	}
	return conn
}

func TestRPCServerDeadlineCancelsQueuedRequestWithoutDisconnect(t *testing.T) {
	server, ep, ops := startCancellationRPC(t)
	_, release, err := server.coordinator.operationContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	conn := rawRPCConnection(t, ep)
	if writeErr := ipc.WriteFrame(conn, ipc.Request{Version: ipc.ProtocolVersion, ID: 1, Method: "AddFeed", Params: map[string]string{"url": "x"}, Deadline: time.Now().Add(30 * time.Millisecond)}); writeErr != nil {
		t.Fatal(writeErr)
	}
	if deadlineErr := conn.SetReadDeadline(time.Now().Add(time.Second)); deadlineErr != nil {
		t.Fatal(deadlineErr)
	}
	if _, readErr := ipc.ReadFrame(conn); readErr != nil {
		t.Fatal(readErr)
	}
	if ops.calls.Load() != 0 {
		t.Fatal("expired queued request ran")
	}
}

func TestRPCDisconnectCancelsActiveAndPipelinedRequests(t *testing.T) {
	_, ep, ops := startCancellationRPC(t)
	conn := rawRPCConnection(t, ep)
	request := ipc.Request{Version: ipc.ProtocolVersion, ID: 1, Method: "AddFeed", Params: map[string]string{"url": "first"}}
	if err := ipc.WriteFrame(conn, request); err != nil {
		t.Fatal(err)
	}
	<-ops.started
	request.ID = 2
	if err := ipc.WriteFrame(conn, request); err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
	select {
	case <-ops.cancelled:
	case <-time.After(time.Second):
		t.Fatal("pending pipeline hid disconnect from active operation")
	}
	if ops.calls.Load() != 1 {
		t.Fatalf("pipelined request ran after disconnect: %d calls", ops.calls.Load())
	}
}

func TestRPCExcessPipeliningIsBoundedAndReleasesFrames(t *testing.T) {
	server, ep, ops := startCancellationRPC(t, 512)
	conn := rawRPCConnection(t, ep)
	request := ipc.Request{Version: ipc.ProtocolVersion, ID: 1, Method: "AddFeed", Params: map[string]string{"url": "x"}}
	if err := ipc.WriteFrame(conn, request); err != nil {
		t.Fatal(err)
	}
	<-ops.started
	for _, id := range []uint64{2, 3} {
		request.ID = id
		if err := ipc.WriteFrame(conn, request); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case <-ops.cancelled:
	case <-time.After(time.Second):
		t.Fatal("excess pipeline was not rejected")
	}
	waitRPCCondition(t, func() bool {
		server.mu.Lock()
		defer server.mu.Unlock()
		return len(server.conns) == 0
	})
	// Use most of the 512-byte budget in a fresh request. Leaked reservations
	// from the rejected pipeline would prevent this connection from working.
	client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	id := string(make([]byte, 60)) // JSON escaping expands this to 360 bytes.
	if callErr := client.Call("GetFeed", map[string]string{"id": id}, nil); !errors.Is(callErr, ipc.ErrNotFound) {
		t.Fatalf("rejected pipeline leaked frame budget: %v", callErr)
	}
}

func TestRPCResponseWriteDeadlineClosesSlowSocket(t *testing.T) {
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	server := &RPCServer{writeTimeout: 20 * time.Millisecond}
	done := make(chan struct{})
	go func() { server.writeResult(serverConn, 1, "small response"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("non-reading client pinned response writer")
	}
}

func TestRPCConnectionLimitPreservesExistingIdleConnections(t *testing.T) {
	_, server, ep, _ := startTestRPC(t, filepath.Join(t.TempDir(), "bounds.db"))
	for range maxRPCConnections {
		client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Close() })
		if callErr := client.Call("FeedStats", nil, nil); callErr != nil {
			t.Fatal(callErr)
		}
	}
	extra, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err == nil {
		_ = extra.Close()
		t.Fatal("connection limit was not enforced")
	}
	server.mu.Lock()
	count := len(server.conns)
	server.mu.Unlock()
	if count != maxRPCConnections {
		t.Fatalf("existing idle connections were evicted: %d", count)
	}
}

func TestRPCServerSharesFrameBudgetAcrossConnections(t *testing.T) {
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	ep := ipc.NewEndpoint(filepath.Join(t.TempDir(), "frame-bounds.db"))
	server, err := NewRPCServer(ep, store, NewMutationCoordinator(&blockingOps{}))
	if err != nil {
		t.Fatal(err)
	}
	server.frameBudget = ipc.NewFrameBudget(512)
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	t.Cleanup(func() { _ = server.Close(); <-serveDone; _ = store.Close() })
	first := rawRPCConnection(t, ep)
	second := rawRPCConnection(t, ep)
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 400)
	if _, err := first.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	// A second partial frame must either be rejected immediately by the shared
	// budget, or (if its header wins the race) reserve the only available slot.
	if _, err := second.Write(header[:]); err != nil {
		t.Fatal(err)
	}
	firstDone, secondDone := make(chan error, 1), make(chan error, 1)
	go func() { _, err := ipc.ReadFrame(first); firstDone <- err }()
	go func() { _, err := ipc.ReadFrame(second); secondDone <- err }()
	select {
	case <-firstDone:
		_ = second.Close()
		<-secondDone
	case <-secondDone:
		_ = first.Close()
		<-firstDone
	case <-time.After(time.Second):
		_ = first.Close()
		_ = second.Close()
		<-firstDone
		<-secondDone
		t.Fatal("aggregate frame budget did not reject excess allocation")
	}
}

func TestRPCAtomicFeedPrimitivesNotifyOnlyOnCommit(t *testing.T) {
	_, _, ep, listener := startTestRPC(t, filepath.Join(t.TempDir(), "feed-primitives.db"))
	client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	remote := storage.NewRPCStore(client)
	defer remote.Close()
	value := &storage.Feed{ID: "f", URL: "https://example.com/rss"}
	if _, commitErr := remote.CommitFeed(value, []*storage.Article{{ID: "a", FeedID: "f"}}, true); commitErr != nil {
		t.Fatal(commitErr)
	}
	if _, deskErr := remote.SetFeedDesk("f", "desk"); deskErr != nil {
		t.Fatal(deskErr)
	}
	if _, renameErr := remote.RenameFeed("f", "custom"); renameErr != nil {
		t.Fatal(renameErr)
	}
	if _, commitErr := remote.CommitFeed(value, nil, false); commitErr != nil {
		t.Fatal(commitErr)
	}
	if _, duplicateErr := remote.CommitFeed(value, nil, true); !errors.Is(duplicateErr, storage.ErrFeedExists) {
		t.Fatal(duplicateErr)
	}
	if _, missingErr := remote.RenameFeed("missing", "custom"); missingErr == nil {
		t.Fatal("missing rename succeeded")
	}
	listener.mu.Lock()
	defer listener.mu.Unlock()
	if listener.feeds != 4 || listener.articles != 1 {
		t.Fatalf("notifications = feeds:%d articles:%d", listener.feeds, listener.articles)
	}
}
