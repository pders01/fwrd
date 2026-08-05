package ipc

import (
	"encoding/json"
	"errors"
	"net"
	"testing"
)

func TestClientRejectsResponseVersionMismatch(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := &Client{conn: clientConn}
	defer client.Close()

	go func() {
		body, err := ReadFrame(serverConn)
		if err != nil {
			return
		}
		var request Request
		if json.Unmarshal(body, &request) != nil {
			return
		}
		_ = WriteFrame(serverConn, Response{Version: ProtocolVersion + 1, ID: request.ID})
	}()

	if err := client.Call("test", nil, nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Call error = %v", err)
	}
}

func TestClientRejectsResponseIDMismatch(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer serverConn.Close()
	client := &Client{conn: clientConn}
	defer client.Close()

	go func() {
		body, err := ReadFrame(serverConn)
		if err != nil {
			return
		}
		var request Request
		if json.Unmarshal(body, &request) != nil {
			return
		}
		_ = WriteFrame(serverConn, Response{Version: ProtocolVersion, ID: request.ID + 1})
	}()

	if err := client.Call("test", nil, nil); !errors.Is(err, ErrProtocol) {
		t.Fatalf("Call error = %v", err)
	}
}
