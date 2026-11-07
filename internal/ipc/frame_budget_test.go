package ipc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

func encodedFrame(body string) []byte {
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame, uint32(len(body)))
	copy(frame[4:], body)
	return frame
}

func TestFrameBudgetHeldUntilRelease(t *testing.T) {
	budget := NewFrameBudget(8)
	body, release, err := ReadFrameWithBudget(bytes.NewReader(encodedFrame("12345678")), MaxFrameSize, budget)
	if err != nil || string(body) != "12345678" {
		t.Fatalf("read = %q, %v", body, err)
	}
	reader := bytes.NewReader(encodedFrame("x"))
	_, rejectedRelease, rejectedErr := ReadFrameWithBudget(reader, MaxFrameSize, budget)
	rejectedRelease()
	if !errors.Is(rejectedErr, ErrFrameBudget) || reader.Len() != 1 {
		t.Fatalf("budget did not reject before reading body: %v, unread=%d", rejectedErr, reader.Len())
	}
	release()
	_, nextRelease, nextErr := ReadFrameWithBudget(bytes.NewReader(encodedFrame("12345678")), MaxFrameSize, budget)
	nextRelease()
	if nextErr != nil || budget.used.Load() != 0 {
		t.Fatalf("budget not reusable: %v, used=%d", nextErr, budget.used.Load())
	}
}

func TestFrameBudgetReleasesTruncatedBodiesAndChecksLimit(t *testing.T) {
	budget := NewFrameBudget(8)
	truncated := encodedFrame("12345678")[:5]
	_, release, err := ReadFrameWithBudget(bytes.NewReader(truncated), MaxFrameSize, budget)
	release()
	if !errors.Is(err, ErrConnClosed) || budget.used.Load() != 0 {
		t.Fatalf("truncated read leaked reservation: %v, used=%d", err, budget.used.Load())
	}
	reader := bytes.NewReader(encodedFrame("12345678"))
	_, release, err = ReadFrameWithBudget(reader, 4, budget)
	release()
	if !errors.Is(err, ErrFrameTooLarge) || reader.Len() != 8 || budget.used.Load() != 0 {
		t.Fatalf("limit did not reject before allocation/read: %v, unread=%d", err, reader.Len())
	}
}

func TestFrameBudgetConcurrentReservations(t *testing.T) {
	budget := NewFrameBudget(4)
	var held atomic.Int64
	var exceeded atomic.Bool
	var wg sync.WaitGroup
	for range 32 {
		wg.Go(func() {
			for range 100 {
				release, err := budget.reserve(1)
				if err != nil {
					if !errors.Is(err, ErrFrameBudget) {
						t.Error(err)
					}
					continue
				}
				if held.Add(1) > 4 {
					exceeded.Store(true)
				}
				held.Add(-1)
				release()
			}
		})
	}
	wg.Wait()
	if exceeded.Load() || budget.used.Load() != 0 {
		t.Fatalf("aggregate budget violated or leaked: exceeded=%v used=%d", exceeded.Load(), budget.used.Load())
	}
}

func TestWriteFrameHandlesShortHeaderAndBodyWrites(t *testing.T) {
	var output bytes.Buffer
	if err := WriteFrame(oneByteWriter{&output}, map[string]string{"x": "value"}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFrame(&output); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
}

type oneByteWriter struct{ io.Writer }

func (w oneByteWriter) Write(data []byte) (int, error) { return w.Writer.Write(data[:1]) }
