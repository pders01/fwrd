package storage

import (
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/ipc"
)

func TestCountedCommitRPCRequiresCapability(t *testing.T) {
	var calls atomic.Int32
	backend := paginationPeer(t, func(conn net.Conn, req *ipc.Request) error {
		calls.Add(1)
		if req.Method != "CommitFeedCounted" && req.Method != "SetFeedError" {
			t.Errorf("unexpected fallback: %s", req.Method)
		}
		return ipc.WriteFrame(conn, ipc.Response{Version: ipc.ProtocolVersion, ID: req.ID, Error: &ipc.ErrorBody{Code: ipc.CodeUnknownMethod, Message: "old server"}})
	})
	result, err := backend.CommitFeedCounted(&Feed{ID: "f"}, []*Article{{ID: "a", FeedID: "f"}}, false)
	if ipc.ErrorCode(err) != ipc.CodeUnknownMethod || result.AddedArticles != 0 || result.Articles != nil {
		t.Fatalf("unsupported counted commit = %+v, %v", result, err)
	}
	if _, err := backend.SetFeedError("f", "failure", time.Now()); ipc.ErrorCode(err) != ipc.CodeUnknownMethod {
		t.Fatalf("unsupported health update = %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("unsupported operations retried: %d calls", calls.Load())
	}
}

func TestCountedCommitRPCDroppedResponseIsNotReplayed(t *testing.T) {
	var calls atomic.Int32
	backend := paginationPeer(t, func(conn net.Conn, _ *ipc.Request) error {
		calls.Add(1)
		return conn.Close()
	})
	result, err := backend.CommitFeedCounted(&Feed{ID: "f"}, []*Article{{ID: "a", FeedID: "f"}}, false)
	if err == nil || result.AddedArticles != 0 || result.Articles != nil || calls.Load() != 1 {
		t.Fatalf("dropped result = %+v, %v, calls=%d", result, err, calls.Load())
	}
}
