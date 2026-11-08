package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// unsentConn can model an already-handshaken connection without relying on a
// kernel's buffering or broken-pipe timing. An optional real connection lets a
// write succeed at the peer and still report an error to the client.
type unsentConn struct {
	net.Conn
	write       func([]byte, int) (int, error)
	readErr     error
	deadlineErr error
	writes      int
	wire        bytes.Buffer
	closed      atomic.Bool
}

func (c *unsentConn) Write(p []byte) (int, error) {
	c.writes++
	if c.closed.Load() {
		return 0, net.ErrClosed
	}
	n, err := len(p), error(nil)
	if c.write != nil {
		n, err = c.write(p, c.writes)
	} else if c.Conn != nil {
		n, err = c.Conn.Write(p)
	}
	if n > 0 && n <= len(p) {
		_, _ = c.wire.Write(p[:n])
	}
	return n, err
}

func (c *unsentConn) Read(p []byte) (int, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	if c.Conn != nil {
		return c.Conn.Read(p)
	}
	return 0, io.EOF
}

func (c *unsentConn) Close() error {
	c.closed.Store(true)
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func (c *unsentConn) SetDeadline(deadline time.Time) error {
	if c.deadlineErr != nil {
		return c.deadlineErr
	}
	if c.Conn != nil {
		return c.Conn.SetDeadline(deadline)
	}
	return nil
}

// All recovery connections go through testPeer's real database handshake.
// Counts are checked after its cleanup has joined every handler, including
// connections which were discarded before dispatching a request.
func unsentRecoveryPeer(t *testing.T, old *unsentConn, wantHandshakes, wantRequests int32) Endpoint {
	t.Helper()
	var handshakes, requests atomic.Int32
	t.Cleanup(func() {
		if got := handshakes.Load(); got != wantHandshakes {
			t.Errorf("recovery handshakes = %d, want %d", got, wantHandshakes)
		}
		if got := requests.Load(); got != wantRequests {
			t.Errorf("server executions = %d, want %d", got, wantRequests)
		}
	})
	return testPeer(t, func(conn net.Conn, _ int) {
		handshakes.Add(1)
		if old != nil && !old.closed.Load() {
			t.Error("reconnected before discarding the failed connection")
		}
		body, err := ReadFrame(conn)
		if err != nil {
			return
		}
		var req Request
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		requests.Add(1)
		_ = WriteFrame(conn, Response{Version: ProtocolVersion, ID: req.ID, Result: json.RawMessage(`true`)})
	})
}

func unsentClient(t *testing.T, ep Endpoint, conn net.Conn) *Client {
	t.Helper()
	client := &Client{addr: ep.Address, databaseID: ep.DatabaseID, clientID: "unsent-test", conn: conn}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestClientUnsentMutationRecoversOnce(t *testing.T) {
	for _, method := range []string{
		"MarkArticleRead", "MarkArticleStarred", "SaveFeed", "SaveArticles",
		"MergeArticles", "DeleteFeed", "RefreshFeed", "RefreshAllFeeds",
		"AddFeed", "ImportFeeds", "unknown",
	} {
		t.Run(method, func(t *testing.T) {
			failed := &unsentConn{write: func([]byte, int) (int, error) { return 0, io.ErrClosedPipe }}
			ep := unsentRecoveryPeer(t, failed, 1, 1)
			client := unsentClient(t, ep, failed)
			var result bool
			if err := client.Call(method, map[string]any{"id": 42}, &result); err != nil {
				t.Fatalf("proven-unsent mutation did not recover: %v", err)
			}
			if !result || failed.writes != 1 || failed.wire.Len() != 0 || !failed.closed.Load() {
				t.Fatalf("result=%v writes=%d bytes=%d closed=%v", result, failed.writes, failed.wire.Len(), failed.closed.Load())
			}
		})
	}
}

func TestClientUnsentDoesNotReplayUncertainWrites(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func([]byte, int) (int, error)
		calls int
	}{
		{"partial header with error", func([]byte, int) (int, error) { return 1, io.ErrClosedPipe }, 1},
		{"partial header then zero error", func(_ []byte, call int) (int, error) {
			if call == 1 {
				return 1, nil
			}
			return 0, io.ErrClosedPipe
		}, 2},
		{"full header with error", func(p []byte, _ int) (int, error) { return len(p), io.ErrClosedPipe }, 1},
		{"header succeeds body fails zero", func(p []byte, call int) (int, error) {
			if call == 1 {
				return len(p), nil
			}
			return 0, io.ErrClosedPipe
		}, 2},
		{"partial body with error", func(p []byte, call int) (int, error) {
			if call == 1 {
				return len(p), nil
			}
			return 1, io.ErrClosedPipe
		}, 2},
		{"partial body then zero error", func(p []byte, call int) (int, error) {
			if call == 1 {
				return len(p), nil
			}
			if call == 2 {
				return 1, nil
			}
			return 0, io.ErrClosedPipe
		}, 3},
		{"negative count with retryable error", func([]byte, int) (int, error) { return -1, io.ErrClosedPipe }, 1},
		{"oversized count with retryable error", func(p []byte, _ int) (int, error) { return len(p) + 1, io.ErrClosedPipe }, 1},
		{"negative count without error", func([]byte, int) (int, error) { return -1, nil }, 1},
		{"oversized count without error", func(p []byte, _ int) (int, error) { return len(p) + 1, nil }, 1},
		{"zero progress without error", func([]byte, int) (int, error) { return 0, nil }, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			failed := &unsentConn{write: test.write}
			// The only real request must be the independent next call below.
			ep := unsentRecoveryPeer(t, failed, 1, 1)
			client := unsentClient(t, ep, failed)
			if err := client.Call("MarkArticleRead", nil, nil); err == nil {
				t.Fatal("uncertain write was replayed or its error was hidden")
			}
			if failed.writes != test.calls || !failed.closed.Load() || client.IsConnected() {
				t.Fatalf("writes=%d want=%d closed=%v connected=%v", failed.writes, test.calls, failed.closed.Load(), client.IsConnected())
			}
			if err := client.Call("MarkArticleStarred", nil, nil); err != nil {
				t.Fatalf("independent next mutation did not recover: %v", err)
			}
		})
	}
}

func TestClientUnsentFullFinalWriteWithErrorIsNotReplayed(t *testing.T) {
	var executions atomic.Int32
	executed := make(chan struct{}, 2)
	ep := testPeer(t, func(conn net.Conn, _ int) {
		if _, err := ReadFrame(conn); err == nil {
			executions.Add(1)
			executed <- struct{}{}
		}
	})
	client, err := NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	wrapped := &unsentConn{Conn: client.conn}
	wrapped.write = func(p []byte, call int) (int, error) {
		n, err := wrapped.Conn.Write(p)
		if call == 2 && err == nil {
			return n, io.ErrClosedPipe
		}
		return n, err
	}
	client.conn = wrapped
	if err := client.Call("MarkArticleStarred", nil, nil); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("final write error = %v", err)
	}
	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		t.Fatal("complete request did not reach the server")
	}
	if executions.Load() != 1 || wrapped.writes != 2 || client.IsConnected() {
		t.Fatalf("executions=%d writes=%d connected=%v", executions.Load(), wrapped.writes, client.IsConnected())
	}
	if _, err := ReadFrame(bytes.NewReader(wrapped.wire.Bytes())); err != nil {
		t.Fatalf("final write did not contain a complete frame: %v", err)
	}
}

type unsentMarshalFunc func() ([]byte, error)

func (f unsentMarshalFunc) MarshalJSON() ([]byte, error) { return f() }

func TestClientUnsentSecondZeroWriteFailureIsBounded(t *testing.T) {
	failed := &unsentConn{write: func([]byte, int) (int, error) { return 0, io.ErrClosedPipe }}
	ep := unsentRecoveryPeer(t, failed, 2, 1)
	client := unsentClient(t, ep, failed)
	var marshals atomic.Int32
	params := unsentMarshalFunc(func() ([]byte, error) {
		if marshals.Add(1) > 1 {
			// connect has installed the real, handshaken retry connection.
			// Closing it before WriteFrame's first Write deterministically
			// produces a second zero-byte transport failure, without a dial hook.
			client.mu.Lock()
			conn := client.conn
			client.mu.Unlock()
			if conn != nil {
				_ = conn.Close()
			}
		}
		return []byte(`{}`), nil
	})
	err := client.CallWithContext(context.Background(), "MarkArticleRead", params, nil, 2*time.Second)
	if err == nil || errors.Is(err, context.DeadlineExceeded) || marshals.Load() != 2 {
		t.Fatalf("bounded recovery: error=%v marshals=%d, want two attempts", err, marshals.Load())
	}
	if client.IsConnected() || !failed.closed.Load() {
		t.Fatal("second failed connection was not discarded")
	}
	if err := client.Call("MarkArticleStarred", nil, nil); err != nil {
		t.Fatalf("independent next mutation did not recover: %v", err)
	}
}

func TestClientUnsentRequiresRetryableError(t *testing.T) {
	terminal := errors.New("terminal write failure")
	failed := &unsentConn{write: func([]byte, int) (int, error) { return 0, terminal }}
	ep := unsentRecoveryPeer(t, failed, 0, 0)
	client := unsentClient(t, ep, failed)
	if err := client.Call("MarkArticleRead", nil, nil); !errors.Is(err, terminal) {
		t.Fatalf("nonretryable zero-write error = %v", err)
	}
	if failed.writes != 1 || client.IsConnected() {
		t.Fatalf("writes=%d connected=%v", failed.writes, client.IsConnected())
	}
}

func TestClientUnsentOnlyActualRequestWriteGrantsRecovery(t *testing.T) {
	for _, phase := range []string{"marshal", "unsupported params", "oversized params", "set deadline", "read response"} {
		t.Run(phase, func(t *testing.T) {
			// This deliberately looks like a zero-byte frame failure, but
			// originates outside the actual request's WriteFrame return.
			forged := &frameWriteError{written: 0, err: io.ErrClosedPipe}
			failed := &unsentConn{}
			var params any
			wantWrites := 0
			switch phase {
			case "marshal":
				params = unsentMarshalFunc(func() ([]byte, error) { return nil, forged })
			case "unsupported params":
				params = func() {}
			case "oversized params":
				params = strings.Repeat("x", MaxFrameSize)
			case "set deadline":
				failed.deadlineErr = forged
			case "read response":
				failed.readErr = forged
				wantWrites = 2
			}
			ep := unsentRecoveryPeer(t, failed, 1, 1)
			client := unsentClient(t, ep, failed)
			err := client.Call("MarkArticleRead", params, nil)
			if err == nil {
				t.Fatal("non-request-write failure was retried or hidden")
			}
			if phase != "unsupported params" && phase != "oversized params" && !errors.Is(err, io.ErrClosedPipe) {
				t.Fatalf("underlying retryable error lost: %v", err)
			}
			if phase == "oversized params" && !errors.Is(err, ErrFrameTooLarge) {
				t.Fatalf("oversized request error = %v", err)
			}
			if failed.writes != wantWrites || !failed.closed.Load() || client.IsConnected() {
				t.Fatalf("writes=%d want=%d closed=%v connected=%v", failed.writes, wantWrites, failed.closed.Load(), client.IsConnected())
			}
			if err := client.Call("MarkArticleStarred", nil, nil); err != nil {
				t.Fatalf("independent call after %s failed: %v", phase, err)
			}
		})
	}
}

func TestClientUnsentCancellationAndClosePreventRecovery(t *testing.T) {
	for _, phase := range []string{"already canceled", "already expired", "cancel on write", "expire on write", "already closed", "close on write"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failed := &unsentConn{}
			ep := unsentRecoveryPeer(t, failed, 0, 0)
			client := unsentClient(t, ep, failed)
			want := error(context.Canceled)
			wantWrites := 0
			switch phase {
			case "already canceled":
				cancel()
			case "already expired":
				var expire context.CancelFunc
				ctx, expire = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer expire()
				want = context.DeadlineExceeded
			case "cancel on write":
				wantWrites = 1
			case "expire on write":
				var expire context.CancelFunc
				ctx, expire = context.WithTimeout(ctx, time.Second)
				defer expire()
				want, wantWrites = context.DeadlineExceeded, 1
			case "already closed":
				_ = client.Close()
				want = ErrConnClosed
			case "close on write":
				wantWrites = 1
				want = ErrConnClosed
			}
			failed.write = func([]byte, int) (int, error) {
				switch phase {
				case "close on write":
					_ = client.Close()
				case "expire on write":
					<-ctx.Done()
				default:
					cancel()
				}
				return 0, io.ErrClosedPipe
			}
			err := client.CallWithContext(ctx, "MarkArticleRead", nil, nil, time.Second)
			// Close during the write may preserve the original write error;
			// it must never reconnect or dispatch another request.
			if phase == "close on write" {
				if err == nil {
					t.Fatal("closed call succeeded")
				}
			} else if !errors.Is(err, want) {
				t.Fatalf("interrupted call error = %v, want %v", err, want)
			}
			if failed.writes != wantWrites {
				t.Fatalf("writes=%d, want %d", failed.writes, wantWrites)
			}
			if wantWrites != 0 && client.IsConnected() {
				t.Fatal("interrupted write connection remained reusable")
			}
		})
	}
}

func TestClientUnsentHandshakeFailureDoesNotDispatchMutation(t *testing.T) {
	for _, recovering := range []bool{false, true} {
		name := "initial connection"
		if recovering {
			name = "after zero-byte request failure"
		}
		t.Run(name, func(t *testing.T) {
			ep := NewEndpoint(filepath.Join(t.TempDir(), "failed-handshake.db"))
			ln, err := ListenEndpoint(ep)
			if err != nil {
				t.Fatal(err)
			}
			var handshakes atomic.Int32
			peerDone := make(chan struct{})
			go func() {
				defer close(peerDone)
				for {
					conn, err := ln.Accept()
					if err != nil {
						return
					}
					_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
					if body, err := ReadFrame(conn); err == nil {
						var hs Handshake
						if err := json.Unmarshal(body, &hs); err != nil || hs.DatabaseID != ep.DatabaseID {
							t.Errorf("expected database handshake, got %s (error=%v)", body, err)
						}
						handshakes.Add(1)
					}
					// Drop the handshake response, not a request response.
					_ = conn.Close()
				}
			}()
			t.Cleanup(func() {
				_ = ln.Close()
				<-peerDone
				if got := handshakes.Load(); got != 1 {
					t.Errorf("handshake attempts=%d, want 1", got)
				}
			})
			client := unsentClient(t, ep, nil)
			var wantRequestID uint64
			if recovering {
				client.conn = &unsentConn{write: func([]byte, int) (int, error) { return 0, io.ErrClosedPipe }}
				wantRequestID = 1
			}
			if err := client.CallWithContext(context.Background(), "MarkArticleRead", nil, nil, 2*time.Second); !errors.Is(err, ErrConnClosed) {
				t.Fatalf("dropped handshake response error = %v", err)
			}
			if client.IsConnected() || client.reqID != wantRequestID {
				t.Fatalf("failed handshake dispatched mutation: connected=%v requestID=%d, want %d", client.IsConnected(), client.reqID, wantRequestID)
			}
		})
	}
}

func TestClientUnsentHandshakeMismatchDoesNotDispatchMutation(t *testing.T) {
	var handshakes, requests atomic.Int32
	t.Cleanup(func() {
		if handshakes.Load() != 1 || requests.Load() != 0 {
			t.Errorf("handshakes=%d executions=%d, want 1 and 0", handshakes.Load(), requests.Load())
		}
	})
	ep := testPeer(t, func(conn net.Conn, _ int) {
		handshakes.Add(1)
		if _, err := ReadFrame(conn); err == nil {
			requests.Add(1)
		}
	})
	client := &Client{addr: ep.Address, databaseID: "wrong-database", clientID: "unsent-test"}
	t.Cleanup(func() { _ = client.Close() })
	if err := client.Call("MarkArticleRead", nil, nil); !errors.Is(err, ErrDatabaseMismatch) {
		t.Fatalf("handshake mismatch error = %v", err)
	}
	if client.IsConnected() || client.reqID != 0 {
		t.Fatalf("failed handshake dispatched a request: connected=%v requestID=%d", client.IsConnected(), client.reqID)
	}
}
