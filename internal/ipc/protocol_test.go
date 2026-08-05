package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

type partialWriter struct {
	buf bytes.Buffer
	max int
}

func (w *partialWriter) Write(p []byte) (int, error) {
	if len(p) > w.max {
		p = p[:w.max]
	}
	return w.buf.Write(p)
}

func TestWriteFrameHandlesPartialWrites(t *testing.T) {
	w := &partialWriter{max: 3}
	want := Handshake{Version: 1, DatabaseID: "partial", Client: "fwrd/test"}
	if err := WriteFrame(w, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	body, err := ReadFrame(&w.buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var got Handshake
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestErrorBodyUnwrapsStableCode(t *testing.T) {
	err := &ErrorBody{Code: CodeMismatch, Message: "wrong database"}
	if !errors.Is(err, ErrDatabaseMismatch) {
		t.Fatalf("errors.Is(%v, ErrDatabaseMismatch) = false", err)
	}
}

func TestWriteReadFrame_RoundTrip(t *testing.T) {
	msg := Handshake{Version: 1, DatabaseID: "abc123", Client: "fwrd/dev"}

	var buf bytes.Buffer
	if err := WriteFrame(&buf, msg); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	body, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}

	var got Handshake
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got != msg {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, msg)
	}
}

func TestReadFrame_TooLarge(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrameSize+1)
	buf := bytes.NewReader(header[:])
	_, err := ReadFrame(buf)
	if err == nil {
		t.Fatal("expected error for oversized frame")
	}
	if !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Errorf("wrong error: %v", err)
	}
}

func TestReadFrame_ZeroLength(t *testing.T) {
	var header [4]byte // all zeros = length 0
	buf := bytes.NewReader(header[:])
	_, err := ReadFrame(buf)
	if !strings.Contains(err.Error(), "zero-length") {
		t.Errorf("wrong error for zero-length frame: %v", err)
	}
}

func TestReadFrame_EOF(t *testing.T) {
	// Header truncated: only 2 bytes available.
	buf := bytes.NewReader([]byte{0, 1})
	_, err := ReadFrame(buf)
	if err == nil {
		t.Fatal("expected error for truncated header")
	}
}

func TestReadFrame_BodyEOF(t *testing.T) {
	// Header says 100 bytes but only 10 are available.
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], 100)
	body := make([]byte, 10)
	buf := bytes.NewReader(append(header[:], body...))
	_, err := ReadFrame(buf)
	if err == nil {
		t.Fatal("expected error for truncated body")
	}
}

func TestWriteFrame_TooLarge(t *testing.T) {
	big := make([]byte, MaxFrameSize+1)
	err := WriteFrame(io.Discard, big)
	if err == nil {
		t.Fatal("expected error for oversized write")
	}
}

func TestRequestResponse_RoundTrip(t *testing.T) {
	req := Request{
		Version: ProtocolVersion,
		ID:      42,
		Method:  "GetFeed",
		Params:  map[string]string{"id": "feed1"},
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, req); err != nil {
		t.Fatalf("WriteFrame request: %v", err)
	}

	// Read back.
	body, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var got Request
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Method != req.Method || got.ID != req.ID {
		t.Errorf("request mismatch: %+v", got)
	}
}

func TestErrorBody(t *testing.T) {
	resp := Response{
		Version: ProtocolVersion,
		ID:      1,
		Error:   &ErrorBody{Code: CodeNotFound, Message: "feed not found"},
	}
	var buf bytes.Buffer
	if err := WriteFrame(&buf, resp); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}

	body, err := ReadFrame(&buf)
	if err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	var got Response
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Error == nil || got.Error.Code != CodeNotFound {
		t.Errorf("error mismatch: %+v", got.Error)
	}
}

func TestIsRetryable(t *testing.T) {
	if !IsRetryable(ErrConnClosed) {
		t.Error("ErrConnClosed should be retryable")
	}
	if !IsRetryable(io.EOF) {
		t.Error("io.EOF should be retryable")
	}
	if IsRetryable(&ErrorBody{Code: CodeMismatch}) {
		t.Error("database mismatch should not be retryable")
	}
}

func TestEndpoint_CanonicalDBPath(t *testing.T) {
	// A simple absolute path. On macOS /tmp is a symlink to /private/tmp,
	// so the resolved path may differ.
	canon, err := CanonicalDBPath("/tmp/test.db")
	if err != nil {
		t.Fatalf("CanonicalDBPath: %v", err)
	}
	if !filepath.IsAbs(canon) {
		t.Errorf("expected absolute path, got %q", canon)
	}
	if filepath.Base(canon) != "test.db" {
		t.Errorf("expected basename test.db, got %q", canon)
	}
}

func TestEndpoint_SameCanonicalSameID(t *testing.T) {
	a, _ := CanonicalDBPath("/tmp/a.db")
	b, _ := CanonicalDBPath("/tmp/../tmp/a.db")
	e1 := NewEndpoint(a)
	e2 := NewEndpoint(b)
	if e1.DatabaseID != e2.DatabaseID {
		t.Errorf("different IDs for same canonical path: %s vs %s", e1.DatabaseID, e2.DatabaseID)
	}
}
