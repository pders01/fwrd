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
	CodeOK             = ""
	CodeNotFound       = "not_found"
	CodeInvalidArg     = "invalid_argument"
	CodeLocked         = "locked"
	CodeUnavailable    = "unavailable"
	CodeInternal       = "internal"
	CodeProtocol       = "protocol_error"
	CodeMismatch       = "database_mismatch"
	CodeUnknownMethod  = "unknown_method"
	CodeResultTooLarge = "result_too_large"
)

// Sentinel errors for common protocol-level problems.
var (
	ErrFrameTooLarge    = errors.New("frame exceeds maximum size")
	ErrZeroLengthFrame  = errors.New("zero-length frame")
	ErrConnClosed       = errors.New("connection closed")
	ErrNotFound         = errors.New("not found")
	ErrInvalidArgument  = errors.New("invalid argument")
	ErrLocked           = errors.New("resource locked")
	ErrUnavailable      = errors.New("service unavailable")
	ErrProtocol         = errors.New("protocol error")
	ErrDatabaseMismatch = errors.New("database mismatch")
	ErrUnknownMethod    = errors.New("unknown method")
	ErrResultTooLarge   = errors.New("result requires pagination")
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
	default:
		return nil
	}
}

// --- framing ---

// WriteFrame marshals msg as JSON, prepends a 4-byte big-endian length,
// and writes the complete frame to w. Frames longer than MaxFrameSize
// are rejected before allocation.
func WriteFrame(w io.Writer, msg any) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if len(data) > MaxFrameSize {
		return fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, len(data))
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(data)))

	frame := make([]byte, 4+len(data))
	copy(frame, header)
	copy(frame[4:], data)

	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return fmt.Errorf("write frame: %w", err)
		}
		if n <= 0 || n > len(frame) {
			return fmt.Errorf("write frame: %w", io.ErrShortWrite)
		}
		frame = frame[n:]
	}
	return nil
}

// ReadFrame reads a 4-byte length prefix and then exactly that many
// bytes from r. Frames larger than MaxFrameSize or zero-length frames
// are rejected.
func ReadFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrConnClosed
		}
		return nil, fmt.Errorf("read header: %w", err)
	}

	length := binary.BigEndian.Uint32(header[:])
	switch {
	case length == 0:
		return nil, ErrZeroLengthFrame
	case length > MaxFrameSize:
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, length)
	}

	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, ErrConnClosed
		}
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

// ReadRequestFrame permits an authenticated connection to remain idle, but
// bounds the time to finish a frame once its first byte arrives. Shutdown can
// still interrupt the idle read by closing conn.
func ReadRequestFrame(conn net.Conn, timeout time.Duration) ([]byte, error) {
	var first [1]byte
	if _, err := io.ReadFull(conn, first[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, ErrConnClosed
		}
		return nil, fmt.Errorf("read first byte: %w", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set frame deadline: %w", err)
	}
	defer conn.SetReadDeadline(time.Time{})
	return ReadFrame(io.MultiReader(bytes.NewReader(first[:]), conn))
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
	return errors.As(err, &opErr) && !opErr.Timeout()
}
