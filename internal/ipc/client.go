package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Timeouts for IPC operations.
const (
	// handshakeTimeout bounds the total handshake exchange (dial +
	// write handshake + read response). Short to fail fast when no
	// server is present.
	handshakeTimeout = 2 * time.Second

	// handshakeProbeTimeout bounds a non-destructive probe to check
	// whether a stale socket has a live server behind it.
	handshakeProbeTimeout = 100 * time.Millisecond

	// defaultCallTimeout is applied to each RPC call. Feed operations
	// may need longer; Store reads are fast.
	defaultCallTimeout = 30 * time.Second
)

// Client is an IPC client with a persistent connection and one in-flight
// request at a time. Calls queue with context-aware deadlines; Close can
// interrupt in-flight I/O without waiting for the request to finish.
type Client struct {
	addr       string
	databaseID string
	clientID   string

	mu         sync.Mutex // protects connection state, never held during I/O
	conn       net.Conn
	reqID      uint64
	closed     bool
	gate       chan struct{}      // serializes calls and reconnects
	done       chan struct{}      // wakes queued calls on Close
	connecting context.CancelFunc // interrupts a dial/handshake on Close
}

// NewClient creates a client, connects, and performs the database
// identity handshake.
func NewClient(addr, databaseID string) (*Client, error) {
	c := &Client{
		addr:       addr,
		databaseID: databaseID,
		clientID:   ClientIdent(),
	}
	if _, err := c.connect(context.Background()); err != nil {
		return nil, err
	}
	return c, nil
}

// connect dials and handshakes within the caller's remaining time budget.
// The caller serializes connection establishment using gate.
func (c *Client) connect(ctx context.Context) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
	defer cancel()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, ErrConnClosed
	}
	c.connecting = cancel
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.connecting = nil
		c.mu.Unlock()
	}()
	conn, err := DialEndpointContext(ctx, c.addr)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	stopCancel := closeOnCancel(ctx, conn)
	defer stopCancel()

	// Set a deadline for the handshake exchange.
	deadline, _ := ctx.Deadline()
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		conn.Close()
		return nil, fmt.Errorf("set deadline: %w", deadlineErr)
	}

	// Send handshake.
	hs := Handshake{
		Version:    ProtocolVersion,
		DatabaseID: c.databaseID,
		Client:     c.clientID,
	}
	if writeErr := WriteFrame(conn, hs); writeErr != nil {
		conn.Close()
		return nil, fmt.Errorf("write handshake: %w", writeErr)
	}

	// Read handshake response.
	body, err := ReadFrame(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("read handshake response: %w", err)
	}

	var resp Handshake
	if err := json.Unmarshal(body, &resp); err != nil {
		conn.Close()
		return nil, fmt.Errorf("unmarshal handshake response: %w", err)
	}

	if resp.Error != nil {
		conn.Close()
		return nil, fmt.Errorf("handshake: %w", resp.Error)
	}
	if resp.Version != ProtocolVersion {
		conn.Close()
		return nil, fmt.Errorf("%w: server=%d client=%d", ErrProtocol, resp.Version, ProtocolVersion)
	}
	if resp.DatabaseID != c.databaseID {
		conn.Close()
		return nil, fmt.Errorf("%w: server db=%s client db=%s", ErrDatabaseMismatch, resp.DatabaseID, c.databaseID)
	}

	// Clear the deadline so subsequent calls set their own.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, fmt.Errorf("clear deadline: %w", err)
	}
	if err := ctx.Err(); err != nil {
		conn.Close()
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		conn.Close()
		return nil, ErrConnClosed
	}
	c.conn = conn
	return conn, nil
}

// Probe attempts a full handshake on addr with a short timeout. It
// returns nil if a matching server responds, or an error otherwise.
// The connection is always closed after the probe.
func Probe(addr, databaseID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeProbeTimeout)
	defer cancel()
	conn, err := DialEndpointContext(ctx, addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	deadline, _ := ctx.Deadline()
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		return deadlineErr
	}

	hs := Handshake{
		Version:    ProtocolVersion,
		DatabaseID: databaseID,
		Client:     ClientIdent(),
	}
	if writeErr := WriteFrame(conn, hs); writeErr != nil {
		return writeErr
	}

	body, err := ReadFrame(conn)
	if err != nil {
		return err
	}

	var resp Handshake
	if err := json.Unmarshal(body, &resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return fmt.Errorf("handshake: %w", resp.Error)
	}
	if resp.Version != ProtocolVersion {
		return fmt.Errorf("%w: server=%d client=%d", ErrProtocol, resp.Version, ProtocolVersion)
	}
	if resp.DatabaseID != databaseID {
		return fmt.Errorf("%w: server db=%s client db=%s", ErrDatabaseMismatch, resp.DatabaseID, databaseID)
	}
	return nil
}

// Call sends a request and waits for a response. Only one call may be
// in flight at a time. Read-only methods retry once on a broken connection.
// A mutation can reconnect once only if its request write accepted zero bytes;
// potentially delivered mutations are never replayed.
func (c *Client) Call(method string, params, result any) error {
	return c.CallWithContext(context.Background(), method, params, result, defaultCallTimeout)
}

// CallWithTimeout is like Call with a custom deadline.
func (c *Client) CallWithTimeout(method string, params, result any, timeout time.Duration) error {
	return c.CallWithContext(context.Background(), method, params, result, timeout)
}

// CallWithContext is like CallWithTimeout and also interrupts network I/O when
// ctx is cancelled. A cancelled mutation is never replayed automatically.
func (c *Client) CallWithContext(ctx context.Context, method string, params, result any, timeout time.Duration) error {
	// Include queueing, reconnecting, and any retry in one total budget.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()

	for attempt := 0; ; attempt++ {
		requestNotSent, err := c.call(ctx, method, params, result)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		// Socket deadlines can fire just before the context's timer callback.
		// Keep timeout errors stable even in that scheduling window.
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			if deadline, ok := ctx.Deadline(); ok && !time.Now().Before(deadline) {
				return context.DeadlineExceeded
			}
		}
		mayRetry := readOnlyMethod(method) || requestNotSent
		if err == nil || attempt != 0 || !mayRetry || !IsRetryable(err) {
			return err
		}
	}
}

func readOnlyMethod(method string) bool {
	switch method {
	case "GetFeed", "GetAllFeeds", "GetArticle", "GetArticlesWithCursor", "FeedStats":
		return true
	default:
		return false
	}
}

// acquire serializes requests without making cancellation or Close wait for
// another operation's network I/O. Lazy initialization also supports test pipes.
func (c *Client) acquire(ctx context.Context) error {
	c.mu.Lock()
	if c.gate == nil {
		c.gate = make(chan struct{}, 1)
		c.done = make(chan struct{})
	}
	gate, done, closed := c.gate, c.done, c.closed
	c.mu.Unlock()
	if closed {
		return ErrConnClosed
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return ErrConnClosed
	case gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			c.release()
			return err
		}
		return nil
	}
}

func (c *Client) release() { <-c.gate }

func (c *Client) discard(conn net.Conn) {
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	c.mu.Unlock()
	_ = conn.Close()
}

// closeOnCancel joins a running cancellation callback before a connection can
// be reused, so late cancellation cannot interfere with the next request.
func closeOnCancel(ctx context.Context, conn net.Conn) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()
		close(done)
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

// call's first result permits recovery only for an actual request transport
// write that accepted zero cumulative frame bytes. Handshake, serialization,
// deadline, response, and invalid-writer failures never grant this permission.
func (c *Client) call(ctx context.Context, method string, params, result any) (bool, error) {
	c.mu.Lock()
	conn, closed := c.conn, c.closed
	c.mu.Unlock()
	if closed {
		return false, ErrConnClosed
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if conn == nil {
		var err error
		conn, err = c.connect(ctx)
		if err != nil {
			return false, fmt.Errorf("reconnect: %w", err)
		}
	}

	healthy := false
	stopCancel := closeOnCancel(ctx, conn)
	defer func() {
		stopCancel()
		if !healthy || ctx.Err() != nil {
			c.discard(conn)
		} else {
			_ = conn.SetDeadline(time.Time{})
		}
	}()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return false, fmt.Errorf("set deadline: %w", err)
	}

	id := atomic.AddUint64(&c.reqID, 1)

	req := Request{
		Version:  ProtocolVersion,
		ID:       id,
		Method:   method,
		Params:   params,
		Deadline: deadline,
	}

	if err := WriteFrame(conn, req); err != nil {
		// Assert the direct type: a marshal error can wrap arbitrary errors
		// but is not evidence of an actual zero-byte socket write.
		writeErr, ok := err.(*frameWriteError)
		return ok && writeErr.written == 0, fmt.Errorf("write request: %w", err)
	}

	body, err := ReadFrame(conn)
	if err != nil {
		return false, fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return false, fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Version != ProtocolVersion {
		return false, fmt.Errorf("%w: response version %d", ErrProtocol, resp.Version)
	}
	if resp.ID != id {
		return false, fmt.Errorf("%w: response ID mismatch: expected %d got %d", ErrProtocol, id, resp.ID)
	}
	if resp.Error != nil && len(resp.Result) != 0 && !bytes.Equal(resp.Result, []byte("null")) {
		return false, fmt.Errorf("%w: response contains both result and error", ErrProtocol)
	}
	healthy = true
	if resp.Error != nil {
		return false, resp.Error
	}

	if result != nil && len(resp.Result) != 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return false, fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return false, nil
}

// Reconnect closes the current connection (if any) and reconnects with a
// fresh handshake. Use after a connection-level error.
func (c *Client) Reconnect() error {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		c.discard(conn)
	}
	_, err := c.connect(ctx)
	return err
}

// Close closes the client and its underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}
	c.closed = true
	if c.connecting != nil {
		c.connecting()
	}
	if c.done != nil {
		close(c.done)
	}
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

// IsConnected reports whether the client has a live connection. It is a
// best-effort check and does not guarantee the next call will succeed.
func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil && !c.closed
}

// ErrorCode extracts the stable code from an error, returning "" for
// non-ErrorBody errors.
func ErrorCode(err error) string {
	var eb *ErrorBody
	if errors.As(err, &eb) {
		return eb.Code
	}
	return ""
}
