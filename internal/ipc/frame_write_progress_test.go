package ipc

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
)

type frameWriteStep struct {
	wantLen int
	n       int
	err     error
}

type frameProgressWriter struct {
	t     *testing.T
	steps []frameWriteStep
	calls int
	data  bytes.Buffer
}

func (w *frameProgressWriter) Write(p []byte) (int, error) {
	w.t.Helper()
	if w.calls >= len(w.steps) {
		w.t.Fatalf("unexpected Write(%d bytes)", len(p))
	}
	step := w.steps[w.calls]
	w.calls++
	if len(p) != step.wantLen {
		w.t.Fatalf("write %d: got %d bytes, want %d", w.calls, len(p), step.wantLen)
	}
	if step.n >= 0 && step.n <= len(p) {
		_, _ = w.data.Write(p[:step.n])
	}
	return step.n, step.err
}

func TestWriteFrameProgressWriterErrors(t *testing.T) {
	cause := &net.OpError{Op: "write", Net: "unix", Err: io.ErrClosedPipe}
	tests := []struct {
		name    string
		steps   []frameWriteStep
		written int
	}{
		{"zero first write", []frameWriteStep{{4, 0, cause}}, 0},
		{"partial header", []frameWriteStep{{4, 2, cause}}, 2},
		{"complete header", []frameWriteStep{{4, 4, cause}}, 4},
		{"zero body", []frameWriteStep{{4, 4, nil}, {6, 0, cause}}, 4},
		{"partial body", []frameWriteStep{{4, 4, nil}, {6, 3, cause}}, 7},
		{"complete body", []frameWriteStep{{4, 4, nil}, {6, 6, cause}}, 10},
		{"short writes accumulate", []frameWriteStep{{4, 1, nil}, {3, 2, nil}, {1, 1, nil}, {6, 2, nil}, {4, 1, nil}, {3, 2, cause}}, 9},
		{"zero after partial header", []frameWriteStep{{4, 1, nil}, {3, 0, cause}}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := &frameProgressWriter{t: t, steps: tt.steps}
			err := WriteFrame(w, "body")
			progress, ok := err.(*frameWriteError)
			if !ok {
				t.Fatalf("got %T (%v), want direct *frameWriteError", err, err)
			}
			if progress.written != tt.written {
				t.Errorf("written = %d, want %d", progress.written, tt.written)
			}
			if progress.Unwrap() != cause || !errors.Is(err, io.ErrClosedPipe) {
				t.Errorf("writer cause not preserved: %v", err)
			}
			var opErr *net.OpError
			if !errors.As(err, &opErr) || opErr != cause {
				t.Errorf("errors.As lost writer cause: %v", err)
			}
			var wrappedProgress *frameWriteError
			if !errors.As(err, &wrappedProgress) || wrappedProgress != progress {
				t.Error("errors.As lost progress error")
			}
			if err.Error() != "write frame: "+cause.Error() {
				t.Errorf("incompatible error text: %q", err.Error())
			}
			if w.calls != len(tt.steps) {
				t.Errorf("writes = %d, want %d", w.calls, len(tt.steps))
			}
		})
	}
}

func TestWriteFrameProgressShortWritesSucceed(t *testing.T) {
	w := &frameProgressWriter{t: t, steps: []frameWriteStep{
		{4, 1, nil}, {3, 2, nil}, {1, 1, nil},
		{6, 2, nil}, {4, 1, nil}, {3, 3, nil},
	}}
	if err := WriteFrame(w, "body"); err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := binary.Write(&want, binary.BigEndian, uint32(6)); err != nil {
		t.Fatal(err)
	}
	want.WriteString(`"body"`)
	if !bytes.Equal(w.data.Bytes(), want.Bytes()) || w.calls != len(w.steps) {
		t.Fatalf("incorrect frame or write count: %x, %d writes", w.data.Bytes(), w.calls)
	}
}

func TestWriteFrameProgressInvalidCounts(t *testing.T) {
	for _, body := range []bool{false, true} {
		for _, n := range []int{-1, 0, 7} {
			for _, cause := range []error{nil, io.ErrClosedPipe} {
				// Zero with an error is a valid writer failure, covered above.
				if n == 0 && cause != nil {
					continue
				}
				name := "header"
				steps := []frameWriteStep{{4, n, cause}}
				if body {
					name = "body"
					steps = []frameWriteStep{{4, 4, nil}, {6, n, cause}}
				}
				t.Run(fmt.Sprintf("%s/n=%d/error=%v", name, n, cause), func(t *testing.T) {
					w := &frameProgressWriter{t: t, steps: steps}
					err := WriteFrame(w, "body")
					var progress *frameWriteError
					if errors.As(err, &progress) {
						t.Fatalf("invalid/zero-progress write marked as progress: %+v", progress)
					}
					if !errors.Is(err, io.ErrShortWrite) {
						t.Fatalf("got %v, want io.ErrShortWrite", err)
					}
					if cause != nil && !errors.Is(err, cause) {
						t.Errorf("underlying cause lost: %v", err)
					}
					if cause == nil && err.Error() != "write frame: short write" {
						t.Errorf("incompatible short-write error: %q", err.Error())
					}
					if n == 0 && IsRetryable(err) {
						t.Error("(0, nil) must not be transport-retryable")
					}
					if w.calls != len(steps) {
						t.Errorf("writes = %d, want %d", w.calls, len(steps))
					}
				})
			}
		}
	}
}

type frameProgressMarshaler struct{ err error }

func (m frameProgressMarshaler) MarshalJSON() ([]byte, error) { return nil, m.err }

func TestWriteFrameProgressPreWriteFailures(t *testing.T) {
	t.Run("marshal", func(t *testing.T) {
		w := &frameProgressWriter{t: t}
		err := WriteFrame(w, make(chan int))
		var progress *frameWriteError
		var unsupported *json.UnsupportedTypeError
		if errors.As(err, &progress) || !errors.As(err, &unsupported) {
			t.Fatalf("unexpected marshal error: %T (%v)", err, err)
		}
		if !strings.HasPrefix(err.Error(), "marshal: ") {
			t.Errorf("incompatible marshal error: %q", err.Error())
		}
	})
	t.Run("marshal wraps progress cause without marking direct progress", func(t *testing.T) {
		w := &frameProgressWriter{t: t}
		cause := &frameWriteError{err: io.ErrClosedPipe}
		err := WriteFrame(w, frameProgressMarshaler{err: cause})
		if _, direct := err.(*frameWriteError); direct {
			t.Fatal("marshal failure must not directly mark write progress")
		}
		var progress *frameWriteError
		var marshalErr *json.MarshalerError
		if !errors.As(err, &marshalErr) || !errors.As(err, &progress) || progress != cause || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("marshal wrapping lost: %v", err)
		}
	})
	t.Run("size", func(t *testing.T) {
		w := &frameProgressWriter{t: t}
		err := WriteFrame(w, strings.Repeat("x", MaxFrameSize))
		var progress *frameWriteError
		if errors.As(err, &progress) || !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("unexpected size error: %T (%v)", err, err)
		}
		if err.Error() != "frame exceeds maximum size: 67108866 bytes" {
			t.Errorf("incompatible size error: %q", err.Error())
		}
	})
}
