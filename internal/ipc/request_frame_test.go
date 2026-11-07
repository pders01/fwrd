package ipc

import (
	"bytes"
	"errors"
	"net"
	"testing"
	"time"
)

func TestReadRequestFrameAllowsIdleBetweenRequests(t *testing.T) {
	reader, writer := net.Pipe()
	defer reader.Close()
	defer writer.Close()
	for range 2 {
		done := make(chan error, 1)
		go func() {
			_, err := ReadRequestFrame(reader, 20*time.Millisecond)
			done <- err
		}()
		select {
		case err := <-done:
			t.Fatalf("idle connection expired before any frame bytes arrived: %v", err)
		case <-time.After(60 * time.Millisecond):
		}
		if err := WriteFrame(writer, Request{Version: ProtocolVersion, ID: 1, Method: "GetAllFeeds"}); err != nil {
			t.Fatal(err)
		}
		if err := awaitCall(t, done); err != nil {
			t.Fatalf("request after idle: %v", err)
		}
	}
}

func TestReadRequestFrameTimesOutPartialFrames(t *testing.T) {
	var encoded bytes.Buffer
	if err := WriteFrame(&encoded, Request{Version: ProtocolVersion, ID: 1, Method: "GetAllFeeds"}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []int{1, 3, 6} {
		t.Run(string(rune('0'+prefix)), func(t *testing.T) {
			reader, writer := net.Pipe()
			defer reader.Close()
			defer writer.Close()
			done := make(chan error, 1)
			go func() { _, err := ReadRequestFrame(reader, 20*time.Millisecond); done <- err }()
			if _, err := writer.Write(encoded.Bytes()[:prefix]); err != nil {
				t.Fatal(err)
			}
			err := awaitCall(t, done)
			var netErr net.Error
			if !errors.As(err, &netErr) || !netErr.Timeout() {
				t.Fatalf("partial frame error = %v, want timeout", err)
			}
		})
	}
}

func TestReadRequestFrameIdleReadCanBeClosed(t *testing.T) {
	reader, writer := net.Pipe()
	defer reader.Close()
	done := make(chan error, 1)
	go func() { _, err := ReadRequestFrame(reader, time.Second); done <- err }()
	_ = writer.Close()
	if err := awaitCall(t, done); !errors.Is(err, ErrConnClosed) {
		t.Fatalf("error = %v", err)
	}
}
