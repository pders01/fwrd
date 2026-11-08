// Package ipc provides local IPC between fwrd processes. The serve
// process listens on a Unix-domain socket (or Windows named pipe) and
// exposes storage and feed-operation methods over a length-prefixed JSON
// protocol.
package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync/atomic"
	"time"
)

// MaxFrameSize limits the size of a single IPC frame to prevent a
// misbehaving or malicious peer from triggering unbounded allocations.
const MaxFrameSize = 64 << 20 // 64 MiB

// Protocol version sent in every handshake and message envelope.
const ProtocolVersion = 1

// Stable error codes returned by the server. Adding codes is backward-
// compatible; removing or renumbering is not.
const (
	CodeOK                 = ""
	CodeNotFound           = "not_found"
	CodeInvalidArg         = "invalid_argument"
	CodeLocked             = "locked"
	CodeUnavailable        = "unavailable"
	CodeInternal           = "internal"
	CodeProtocol           = "protocol_error"
	CodeMismatch           = "database_mismatch"
	CodeUnknownMethod      = "unknown_method"
	CodeResultTooLarge     = "result_too_large"
	CodeFeedExists         = "feed_exists"
	CodeArticleFeedChanged = "article_feed_changed"
)

// Sentinel errors for common protocol-level problems.
var (
	ErrFrameTooLarge      = errors.New("frame exceeds maximum size")
	ErrFrameBudget        = errors.New("aggregate frame budget exhausted")
	ErrZeroLengthFrame    = errors.New("zero-length frame")
	ErrConnClosed         = errors.New("connection closed")
	ErrNotFound           = errors.New("not found")
	ErrInvalidArgument    = errors.New("invalid argument")
	ErrLocked             = errors.New("resource locked")
	ErrUnavailable        = errors.New("service unavailable")
	ErrProtocol           = errors.New("protocol error")
	ErrDatabaseMismatch   = errors.New("database mismatch")
	ErrUnknownMethod      = errors.New("unknown method")
	ErrResultTooLarge     = errors.New("result requires pagination")
	ErrFeedExists         = errors.New("feed already exists")
	ErrArticleFeedChanged = errors.New("article feed cannot be changed")
)

// --- wire messages ---

// Handshake is the initial message exchanged after connecting.
type Handshake struct {
	Version    int        `json:"version"`
	DatabaseID string     `json:"database_id"`
	Client     string     `json:"client"`
	Error      *ErrorBody `json:"error,omitempty"`
}

// Request is a single RPC call sent from client to server.
type Request struct {
	Version int    `json:"version"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
	// Deadline is an optional absolute request deadline. Older peers ignore it.
	Deadline time.Time `json:"deadline,omitzero"`
}

// Response is the server's reply to a Request.
type Response struct {
	Version int             `json:"version"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *ErrorBody      `json:"error,omitempty"`
}

// ErrorBody carries a stable code and a human-readable message.
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error implements the error interface.
func (e *ErrorBody) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// Unwrap maps stable wire error codes to local sentinel errors while
// preserving the server's human-readable message.
func (e *ErrorBody) Unwrap() error {
	switch e.Code {
	case CodeNotFound:
		return ErrNotFound
	case CodeInvalidArg:
		return ErrInvalidArgument
	case CodeLocked:
		return ErrLocked
	case CodeUnavailable:
		return ErrUnavailable
	case CodeProtocol:
		return ErrProtocol
	case CodeMismatch:
		return ErrDatabaseMismatch
	case CodeUnknownMethod:
		return ErrUnknownMethod
	case CodeResultTooLarge:
		return ErrResultTooLarge
	case CodeFeedExists:
		return ErrFeedExists
	case CodeArticleFeedChanged:
		return ErrArticleFeedChanged
	default:
		return nil
	}
}

// --- framing ---

// frameWriteError records confirmed progress only for writer errors with valid
// byte counts. Only a direct *frameWriteError with written == 0 establishes
// that no frame bytes were sent.
type frameWriteError struct {
	written int
	err     error
}

func (e *frameWriteError) Error() string { return fmt.Sprintf("write frame: %v", e.err) }
func (e *frameWriteError) Unwrap() error { return e.err }

// WriteFrame marshals msg as JSON, prepends a 4-byte big-endian length,
// and writes the complete frame to w. Frames longer than MaxFrameSize
// are rejected before writing.
func WriteFrame(w io.Writer, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > MaxFrameSize {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(data))
	}

	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(data)))
	// Do not allocate a second full-sized copy just to prepend the header.
	written := 0
	for _, part := range [][]byte{header[:], data} {
		for len(part) > 0 {
			n, writeErr := w.Write(part)
			if n < 0 || n > len(part) {
				// Invalid counts cannot establish how many bytes were sent.
				cause := error(io.ErrShortWrite)
				if writeErr != nil {
					cause = errors.Join(cause, writeErr)
				}
				return fmt.Errorf("write frame: %w", cause)
			}
			written += n
			if writeErr != nil {
				return &frameWriteError{written: written, err: writeErr}
			}
			if n == 0 {
				return fmt.Errorf("write frame: %w", io.ErrShortWrite)
			}
			part = part[n:]
		}
	}
	return nil
}

// ReadFrame reads a 4-byte length prefix and then exactly that many
// bytes from r. Frames larger than MaxFrameSize or zero-length frames
// are rejected.
func ReadFrame(r io.Reader) ([]byte, error) {
	body, release, err := ReadFrameWithBudget(r, MaxFrameSize, nil)
	release()
	return body, err
}

// FrameBudget bounds aggregate live frame bodies across connections. Exhaustion
// rejects a frame before allocation instead of blocking while holding resources.
type FrameBudget struct {
	limit int64
	used  atomic.Int64
}

// NewFrameBudget constructs a shared byte budget.
func NewFrameBudget(limit int64) *FrameBudget { return &FrameBudget{limit: limit} }

func (b *FrameBudget) reserve(size int64) (func(), error) {
	if b == nil {
		return func() {}, nil
	}
	for {
		used := b.used.Load()
		if size > b.limit-used {
			return func() {}, ErrFrameBudget
		}
		if b.used.CompareAndSwap(used, used+size) {
			return func() { b.used.Add(-size) }, nil
		}
	}
}

// ReadFrameWithBudget checks both a per-frame limit and a shared aggregate
// budget before allocating. The returned release must be called exactly once
// after the body (and any zero-copy decoded request fields) is no longer used.
// Release is always non-nil, even on error.
func ReadFrameWithBudget(r io.Reader, maxSize uint32, budget *FrameBudget) (body []byte, release func(), err error) {
	noop := func() {}
	var header [4]byte
	if _, readErr := io.ReadFull(r, header[:]); readErr != nil {
		if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
			return nil, noop, ErrConnClosed
		}
		return nil, noop, fmt.Errorf("read header: %w", readErr)
	}

	length := binary.BigEndian.Uint32(header[:])
	switch {
	case length == 0:
		return nil, noop, ErrZeroLengthFrame
	case length > maxSize || length > MaxFrameSize:
		return nil, noop, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, length)
	}
	release, err = budget.reserve(int64(length))
	if err != nil {
		return nil, noop, err
	}
	body = make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		release()
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, noop, ErrConnClosed
		}
		return nil, noop, fmt.Errorf("read body: %w", err)
	}
	return body, release, nil
}

// ReadRequestFrame permits an authenticated connection to remain idle, but
// bounds the time to finish a frame once its first byte arrives. Shutdown can
// still interrupt the idle read by closing conn.
func ReadRequestFrame(conn net.Conn, timeout time.Duration) ([]byte, error) {
	body, release, err := ReadRequestFrameWithBudget(conn, timeout, nil)
	release()
	return body, err
}

// ReadRequestFrameWithBudget permits idle connections while sharing an aggregate
// frame budget. See ReadFrameWithBudget for release ownership.
func ReadRequestFrameWithBudget(conn net.Conn, timeout time.Duration, budget *FrameBudget) (body []byte, release func(), err error) {
	noop := func() {}
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, noop, ErrConnClosed
		}
		return nil, noop, fmt.Errorf("read first byte: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, noop, fmt.Errorf("set frame deadline: %w", err)
	}
	defer func() { _ = conn.SetReadDeadline(time.Time{}) }()
	return ReadFrameWithBudget(io.MultiReader(bytes.NewReader(first[:]), conn), MaxFrameSize, budget)
}

// --- helpers ---

// IsRetryable reports whether err indicates a connection-level failure
// where a fresh handshake might succeed. Protocol errors and data
// rejections (mismatch, unknown method) are not retryable.
func IsRetryable(err error) bool {
	if errors.Is(err, ErrConnClosed) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
		return true
	}
	var opErr *net.OpError
	return (errors.As(err, &opErr) && !opErr.Timeout()) || retryableTransportError(err)
}
