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
// request at a time. It is safe for concurrent use: the mutex serializes
// all calls.
type Client struct {
	addr       string
	databaseID string
	clientID   string

	mu     sync.Mutex
	conn   net.Conn
	reqID  uint64
	closed bool
}

// NewClient creates a client, connects, and performs the database
// identity handshake.
func NewClient(addr, databaseID string) (*Client, error) {
	c := &Client{
		addr:       addr,
		databaseID: databaseID,
		clientID:   ClientIdent(),
	}
	if err := c.connect(); err != nil {
		return nil, err
	}
	return c, nil
}

// connect dials and performs the handshake. Caller must hold c.mu.
func (c *Client) connect() error {
	conn, err := DialEndpoint(c.addr)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	// Set a deadline for the handshake exchange.
	deadline := time.Now().Add(handshakeTimeout)
	if deadlineErr := conn.SetDeadline(deadline); deadlineErr != nil {
		conn.Close()
		return fmt.Errorf("set deadline: %w", deadlineErr)
	}

	// Send handshake.
	hs := Handshake{
		Version:    ProtocolVersion,
		DatabaseID: c.databaseID,
		Client:     c.clientID,
	}
	if writeErr := WriteFrame(conn, hs); writeErr != nil {
		conn.Close()
		return fmt.Errorf("write handshake: %w", writeErr)
	}

	// Read handshake response.
	body, err := ReadFrame(conn)
	if err != nil {
		conn.Close()
		return fmt.Errorf("read handshake response: %w", err)
	}

	var resp Handshake
	if err := json.Unmarshal(body, &resp); err != nil {
		conn.Close()
		return fmt.Errorf("unmarshal handshake response: %w", err)
	}

	if resp.Error != nil {
		conn.Close()
		return fmt.Errorf("handshake: %w", resp.Error)
	}
	if resp.Version != ProtocolVersion {
		conn.Close()
		return fmt.Errorf("%w: server=%d client=%d", ErrProtocol, resp.Version, ProtocolVersion)
	}
	if resp.DatabaseID != c.databaseID {
		conn.Close()
		return fmt.Errorf("%w: server db=%s client db=%s", ErrDatabaseMismatch, resp.DatabaseID, c.databaseID)
	}

	// Clear the deadline so subsequent calls set their own.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return fmt.Errorf("clear deadline: %w", err)
	}

	c.conn = conn
	return nil
}

// Probe attempts a full handshake on addr with a short timeout. It
// returns nil if a matching server responds, or an error otherwise.
// The connection is always closed after the probe.
func Probe(addr, databaseID string) error {
	conn, err := DialEndpoint(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	deadline := time.Now().Add(handshakeProbeTimeout)
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
// in flight at a time. A failed call does not automatically reconnect
// or retry; the caller may call Reconnect and retry explicitly.
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
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// A call made after a previous connection failure establishes a fresh
	// connection, but never replays the failed call that lost the connection.
	if c.conn == nil {
		if err := c.connect(); err != nil {
			return fmt.Errorf("reconnect: %w", err)
		}
	}

	deadline := time.Now().Add(timeout)
	if ctxDeadline, ok := ctx.Deadline(); ok && ctxDeadline.Before(deadline) {
		deadline = ctxDeadline
	}
	if deadlineErr := c.conn.SetDeadline(deadline); deadlineErr != nil {
		return fmt.Errorf("set deadline: %w", deadlineErr)
	}
	conn := c.conn
	stopCancel := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer func() {
		stopCancel()
		if c.conn != nil {
			_ = c.conn.SetDeadline(time.Time{})
		}
	}()

	id := atomic.AddUint64(&c.reqID, 1)

	req := Request{
		Version: ProtocolVersion,
		ID:      id,
		Method:  method,
		Params:  params,
	}

	if err := WriteFrame(c.conn, req); err != nil {
		// Connection is likely broken; mark it unusable.
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("write request: %w", err)
	}

	body, err := ReadFrame(c.conn)
	if err != nil {
		c.conn.Close()
		c.conn = nil
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("read response: %w", err)
	}

	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("unmarshal response: %w", err)
	}

	if resp.Version != ProtocolVersion {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("%w: response version %d", ErrProtocol, resp.Version)
	}
	if resp.ID != id {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("%w: response ID mismatch: expected %d got %d", ErrProtocol, id, resp.ID)
	}
	if resp.Error != nil && len(resp.Result) != 0 && !bytes.Equal(resp.Result, []byte("null")) {
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("%w: response contains both result and error", ErrProtocol)
	}
	if resp.Error != nil {
		return resp.Error
	}

	if result != nil && len(resp.Result) != 0 {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("unmarshal result: %w", err)
		}
	}
	return nil
}

// Reconnect closes the current connection (if any) and reconnects with a
// fresh handshake. Use after a connection-level error.
func (c *Client) Reconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return ErrConnClosed
	}

	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}

	return c.connect()
}

// Close closes the client and its underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
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
