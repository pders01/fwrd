package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func pipeClient(t *testing.T) (*Client, net.Conn) {
	t.Helper()
	clientConn, serverConn := net.Pipe()
	client := &Client{conn: clientConn}
	t.Cleanup(func() { _ = client.Close(); _ = serverConn.Close() })
	return client, serverConn
}

func awaitCall(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("call did not finish promptly")
		return nil
	}
}

func blockedCall(t *testing.T, client *Client, server net.Conn) <-chan error {
	t.Helper()
	started := make(chan struct{})
	go func() {
		_, _ = ReadFrame(server)
		close(started)
		// Wait for the client to close rather than send a response.
		_, _ = ReadFrame(server)
	}()
	done := make(chan error, 1)
	go func() { done <- client.CallWithTimeout("blocked", nil, nil, 5*time.Second) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not reach peer")
	}
	return done
}

func TestClientQueuedCallsRespectCancellationAndTimeout(t *testing.T) {
	for _, test := range []struct {
		name          string
		cancelContext bool
	}{
		{"context", true},
		{"call timeout", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			client, server := pipeClient(t)
			first := blockedCall(t, client, server)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			timeout := 30 * time.Millisecond
			if test.cancelContext {
				timeout = time.Second
				cancel()
			}
			done := make(chan error, 1)
			go func() { done <- client.CallWithContext(ctx, "GetAllFeeds", nil, nil, timeout) }()
			want := context.DeadlineExceeded
			if test.cancelContext {
				want = context.Canceled
			}
			if err := awaitCall(t, done); !errors.Is(err, want) {
				t.Fatalf("queued error = %v, want %v", err, want)
			}
			if !client.IsConnected() {
				t.Fatal("queued cancellation closed the active request's connection")
			}
			_ = client.Close()
			if err := awaitCall(t, first); err == nil {
				t.Fatal("blocked call succeeded after Close")
			}
		})
	}
}

func TestClientCloseInterruptsActiveAndQueuedCalls(t *testing.T) {
	client, server := pipeClient(t)
	first := blockedCall(t, client, server)
	queued := make(chan error, 1)
	go func() { queued <- client.Call("GetAllFeeds", nil, nil) }()
	closed := make(chan error, 1)
	go func() { closed <- client.Close() }()
	if err := awaitCall(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := awaitCall(t, first); err == nil {
		t.Fatal("active call unexpectedly succeeded")
	}
	if err := awaitCall(t, queued); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("queued error = %v", err)
	}
	if client.IsConnected() {
		t.Fatal("closed client reports connected")
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

type writeSignalConn struct {
	net.Conn
	started chan struct{}
}

func (c *writeSignalConn) Write(p []byte) (int, error) {
	close(c.started)
	return c.Conn.Write(p)
}

func TestClientCancellationInterruptsBlockedIO(t *testing.T) {
	for _, phase := range []string{"write", "read"} {
		t.Run(phase, func(t *testing.T) {
			client, server := pipeClient(t)
			started := make(chan struct{})
			if phase == "write" {
				client.conn = &writeSignalConn{Conn: client.conn, started: started}
			} else {
				go func() {
					_, _ = ReadFrame(server)
					close(started)
					_, _ = ReadFrame(server)
				}()
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- client.CallWithContext(ctx, "SaveFeed", nil, nil, time.Second) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("I/O never started")
			}
			cancel()
			if err := awaitCall(t, done); !errors.Is(err, context.Canceled) {
				t.Fatalf("error = %v", err)
			}
			if client.IsConnected() {
				t.Fatal("cancelled I/O left a potentially desynchronized connection reusable")
			}
		})
	}
}

// testPeer implements just the handshake; each accepted connection is owned by
// handler after it. All goroutines and connections are joined/closed on cleanup.
func testPeer(t *testing.T, handler func(net.Conn, int)) Endpoint {
	t.Helper()
	ep := NewEndpoint(filepath.Join(t.TempDir(), "peer.db"))
	ln, err := ListenEndpoint(ep)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	acceptDone := make(chan struct{})
	go func() {
		defer close(acceptDone)
		for index := 0; ; index++ {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				if _, err := ReadFrame(conn); err != nil {
					return
				}
				if err := WriteFrame(conn, Handshake{Version: ProtocolVersion, DatabaseID: ep.DatabaseID}); err != nil {
					return
				}
				handler(conn, index)
			}(index)
		}
	}()
	t.Cleanup(func() { _ = ln.Close(); <-acceptDone; wg.Wait() })
	return ep
}

func TestClientRetriesOnlyReadOnlyTransportFailures(t *testing.T) {
	for _, test := range []struct {
		method   string
		readOnly bool
	}{
		{"GetFeed", true}, {"GetAllFeeds", true}, {"GetArticle", true},
		{"GetArticlesWithCursor", true}, {"FeedStats", true},
		{"SaveFeed", false}, {"SaveArticles", false}, {"MergeArticles", false},
		{"DeleteFeed", false}, {"MarkArticleRead", false}, {"MarkArticleStarred", false},
		{"RefreshFeed", false}, {"RefreshAllFeeds", false}, {"AddFeed", false},
		{"ImportFeeds", false}, {"unknown", false},
	} {
		t.Run(test.method, func(t *testing.T) {
			var requests atomic.Int32
			ep := testPeer(t, func(conn net.Conn, index int) {
				body, err := ReadFrame(conn)
				if err != nil {
					return
				}
				var req Request
				if json.Unmarshal(body, &req) != nil {
					return
				}
				requests.Add(1)
				if index == 0 {
					return
				} // simulate response loss after execution
				_ = WriteFrame(conn, Response{Version: ProtocolVersion, ID: req.ID})
			})
			client, err := NewClient(ep.Address, ep.DatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			err = client.Call(test.method, nil, nil)
			if test.readOnly {
				if err != nil || requests.Load() != 2 {
					t.Fatalf("read retry: requests=%d error=%v", requests.Load(), err)
				}
			} else if err == nil || requests.Load() != 1 {
				t.Fatalf("mutation replayed or failure hidden: requests=%d error=%v", requests.Load(), err)
			}
		})
	}
}

func TestClientRecoversFromAnAlreadyClosedSocket(t *testing.T) {
	closed := make(chan struct{})
	ep := testPeer(t, func(conn net.Conn, index int) {
		if index == 0 {
			_ = conn.Close()
			close(closed)
			return
		}
		body, err := ReadFrame(conn)
		if err != nil {
			return
		}
		var req Request
		if json.Unmarshal(body, &req) != nil {
			return
		}
		_ = WriteFrame(conn, Response{Version: ProtocolVersion, ID: req.ID})
	})
	client, err := NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	<-closed
	if err := client.Call("GetArticlesWithCursor", nil, nil); err != nil {
		t.Fatalf("stale socket was not recovered: %v", err)
	}
}

func TestClientReconnectHandshakeRespectsDeadlineAndClose(t *testing.T) {
	for _, interrupt := range []string{"deadline", "close"} {
		t.Run(interrupt, func(t *testing.T) {
			ep := NewEndpoint(filepath.Join(t.TempDir(), "stalled.db"))
			ln, err := ListenEndpoint(ep)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			started := make(chan struct{})
			peerDone := make(chan struct{})
			go func() {
				defer close(peerDone)
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					return
				}
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
				_, _ = ReadFrame(conn)
				close(started)
				_, _ = ReadFrame(conn) // deliberately withhold handshake response
			}()
			client := &Client{addr: ep.Address, databaseID: ep.DatabaseID}
			defer client.Close()
			timeout := 50 * time.Millisecond
			if interrupt == "close" {
				timeout = 5 * time.Second
			}
			done := make(chan error, 1)
			go func() { done <- client.CallWithTimeout("AddFeed", nil, nil, timeout) }()
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("handshake did not start")
			}
			if interrupt == "close" {
				_ = client.Close()
			}
			err = awaitCall(t, done)
			if err == nil || (interrupt == "deadline" && !errors.Is(err, context.DeadlineExceeded)) {
				t.Fatalf("interrupted handshake: %v", err)
			}
			if client.IsConnected() {
				t.Fatal("interrupted handshake installed a connection")
			}
			select {
			case <-peerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("handshake connection was not released")
			}
		})
	}
}

func TestClientReadRetryIsBounded(t *testing.T) {
	var requests atomic.Int32
	ep := testPeer(t, func(conn net.Conn, _ int) {
		if _, err := ReadFrame(conn); err == nil {
			requests.Add(1)
		}
	})
	client, err := NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Call("GetAllFeeds", nil, nil); err == nil {
		t.Fatal("lost responses unexpectedly succeeded")
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestClientConcurrentCallsStaySerialized(t *testing.T) {
	client, server := pipeClient(t)
	const count = 64
	peerDone := make(chan error, 1)
	go func() {
		for range count {
			body, err := ReadFrame(server)
			if err != nil {
				peerDone <- err
				return
			}
			var req Request
			if decodeErr := json.Unmarshal(body, &req); decodeErr != nil {
				peerDone <- decodeErr
				return
			}
			result, err := json.Marshal(req.Params)
			if err != nil {
				peerDone <- err
				return
			}
			if err := WriteFrame(server, Response{Version: ProtocolVersion, ID: req.ID, Result: result}); err != nil {
				peerDone <- err
				return
			}
		}
		peerDone <- nil
	}()
	results := make(chan error, count)
	for i := range count {
		go func() {
			var got int
			err := client.Call("echo", i, &got)
			if err == nil && got != i {
				err = errors.New("response delivered to the wrong caller")
			}
			results <- err
		}()
	}
	for range count {
		if err := awaitCall(t, results); err != nil {
			t.Fatal(err)
		}
	}
	if err := awaitCall(t, peerDone); err != nil {
		t.Fatal(err)
	}
}

func TestClientReconnectsAfterCancelledRequest(t *testing.T) {
	started := make(chan struct{})
	ep := testPeer(t, func(conn net.Conn, index int) {
		body, err := ReadFrame(conn)
		if err != nil {
			return
		}
		var req Request
		if json.Unmarshal(body, &req) != nil {
			return
		}
		if index == 0 {
			close(started)
			_, _ = ReadFrame(conn)
			return
		}
		_ = WriteFrame(conn, Response{Version: ProtocolVersion, ID: req.ID})
	})
	client, err := NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.CallWithContext(ctx, "SaveFeed", nil, nil, time.Second) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("request never started")
	}
	cancel()
	if err := awaitCall(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled error = %v", err)
	}
	if err := client.Call("GetAllFeeds", nil, nil); err != nil {
		t.Fatalf("next request did not recover: %v", err)
	}
}

func TestClientDoesNotRetryProtocolOrApplicationErrors(t *testing.T) {
	for _, protocolError := range []bool{false, true} {
		t.Run(map[bool]string{false: "application", true: "protocol"}[protocolError], func(t *testing.T) {
			var requests atomic.Int32
			ep := testPeer(t, func(conn net.Conn, _ int) {
				body, err := ReadFrame(conn)
				if err != nil {
					return
				}
				var req Request
				if json.Unmarshal(body, &req) != nil {
					return
				}
				requests.Add(1)
				resp := Response{Version: ProtocolVersion, ID: req.ID, Error: &ErrorBody{Code: CodeUnavailable, Message: "try later"}}
				if protocolError {
					resp.ID++
				}
				_ = WriteFrame(conn, resp)
			})
			client, err := NewClient(ep.Address, ep.DatabaseID)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if err := client.Call("GetAllFeeds", nil, nil); err == nil {
				t.Fatal("expected error")
			}
			if requests.Load() != 1 {
				t.Fatalf("requests = %d, want 1", requests.Load())
			}
		})
	}
}
