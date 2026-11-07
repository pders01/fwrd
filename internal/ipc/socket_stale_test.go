//go:build !windows

package ipc

import (
	"net"
	"os"
	"testing"
)

func TestAmbiguousLiveSocketsAreNeverUnlinked(t *testing.T) {
	for _, behavior := range []string{"silent", "eof", "malformed"} {
		t.Run(behavior, func(t *testing.T) {
			ep := testEndpoint(t, behavior)
			if err := ensurePrivateRunDir(RunDir()); err != nil {
				t.Fatal(err)
			}
			ln, err := net.Listen("unix", ep.Address)
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			accepted := make(chan net.Conn, 1)
			go func() {
				conn, acceptErr := ln.Accept()
				if acceptErr != nil {
					accepted <- nil
					return
				}
				accepted <- conn
				switch behavior {
				case "eof":
					_ = conn.Close()
				case "malformed":
					_, _ = ReadFrame(conn)
					_ = WriteFrame(conn, "not a handshake")
				}
			}()
			before, err := os.Lstat(ep.Address)
			if err != nil {
				t.Fatal(err)
			}
			if removeErr := removeStaleSocket(ep); removeErr == nil {
				t.Fatal("ambiguous socket was treated as stale")
			}
			conn := <-accepted
			if conn != nil {
				_ = conn.Close()
			}
			after, statErr := os.Lstat(ep.Address)
			if statErr != nil || !os.SameFile(before, after) {
				t.Fatalf("live socket was replaced/unlinked: %v", statErr)
			}
		})
	}
}

func TestConnectionRefusedSocketCanBeReplaced(t *testing.T) {
	ep := testEndpoint(t, "refused")
	if err := ensurePrivateRunDir(RunDir()); err != nil {
		t.Fatal(err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: ep.Address, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	replacement, err := ListenEndpoint(ep)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := replacement.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if _, statErr := os.Lstat(ep.Address); !os.IsNotExist(statErr) {
		t.Fatalf("owned listener did not remove its closed socket: %v", statErr)
	}
}

func TestClosingListenerPreservesReplacementSocket(t *testing.T) {
	ep := testEndpoint(t, "replacement")
	ln, err := ListenEndpoint(ep)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if removeErr := os.Remove(ep.Address); removeErr != nil {
		t.Fatal(removeErr)
	}
	replacement, err := net.Listen("unix", ep.Address)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	before, err := os.Lstat(ep.Address)
	if err != nil {
		t.Fatal(err)
	}
	if closeErr := ln.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	after, statErr := os.Lstat(ep.Address)
	if statErr != nil || !os.SameFile(before, after) {
		t.Fatalf("closing old listener removed replacement: %v", statErr)
	}
	if removeErr := RemoveEndpoint(ep.Address); removeErr == nil {
		t.Fatal("RemoveEndpoint unlinked a live listener")
	}
}
