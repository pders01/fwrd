package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pders01/fwrd/internal/ipc"
)

func paginationPeer(t *testing.T, respond func(net.Conn, *ipc.Request) error) *rpcBackend {
	t.Helper()
	ep := ipc.NewEndpoint(filepath.Join(t.TempDir(), "pagination-peer.db"))
	ln, err := ipc.ListenEndpoint(ep)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			done <- acceptErr
			return
		}
		defer conn.Close()
		if _, readErr := ipc.ReadFrame(conn); readErr != nil {
			done <- readErr
			return
		}
		if writeErr := ipc.WriteFrame(conn, ipc.Handshake{Version: ipc.ProtocolVersion, DatabaseID: ep.DatabaseID}); writeErr != nil {
			done <- writeErr
			return
		}
		for {
			body, readErr := ipc.ReadFrame(conn)
			if errors.Is(readErr, ipc.ErrConnClosed) {
				done <- nil
				return
			}
			if readErr != nil {
				done <- readErr
				return
			}
			var req ipc.Request
			if decodeErr := json.Unmarshal(body, &req); decodeErr != nil {
				done <- decodeErr
				return
			}
			if responseErr := respond(conn, &req); responseErr != nil {
				done <- responseErr
				return
			}
		}
	}()
	client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = ln.Close()
		if peerErr := <-done; peerErr != nil {
			t.Error(peerErr)
		}
	})
	return newRPCBackend(client)
}

func TestRPCArticlePaginationReducesOversizedPagesWithoutLosingCursor(t *testing.T) {
	var mu sync.Mutex
	var requested []int
	articles := []*Article{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	backend := paginationPeer(t, func(conn net.Conn, req *ipc.Request) error {
		params := req.Params.(map[string]any)
		limit := int(params["limit"].(float64))
		cursor := params["cursor"].(string)
		mu.Lock()
		requested = append(requested, limit)
		mu.Unlock()
		response := ipc.Response{Version: ipc.ProtocolVersion, ID: req.ID}
		if limit > 1 {
			if cursor != "" {
				return fmt.Errorf("oversize retry unexpectedly advanced cursor to %q", cursor)
			}
			response.Error = &ipc.ErrorBody{Code: ipc.CodeResultTooLarge, Message: "reduce page"}
		} else {
			start := 0
			for i, article := range articles {
				if article.ID == cursor {
					start = i + 1
				}
			}
			data, marshalErr := json.Marshal(articles[start:min(len(articles), start+limit)])
			if marshalErr != nil {
				return marshalErr
			}
			response.Result = data
		}
		return ipc.WriteFrame(conn, response)
	})
	got, err := backend.GetArticlesWithCursor("", 0, "")
	if err != nil || len(got) != 3 || got[0].ID != "a" || got[1].ID != "b" || got[2].ID != "c" {
		t.Fatalf("adaptive pagination = %+v, %v", got, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if requested[0] != 1000 || requested[len(requested)-1] != 1 || len(requested) > 15 {
		t.Fatalf("page reductions = %v", requested)
	}
}

func TestRPCArticlePaginationReportsSingleItemFailure(t *testing.T) {
	backend := paginationPeer(t, func(conn net.Conn, req *ipc.Request) error {
		return ipc.WriteFrame(conn, ipc.Response{Version: ipc.ProtocolVersion, ID: req.ID, Error: &ipc.ErrorBody{Code: ipc.CodeResultTooLarge, Message: "large item"}})
	})
	_, err := backend.GetArticlesWithCursor("f", 1, "previous")
	if !errors.Is(err, ipc.ErrResultTooLarge) || !strings.Contains(err.Error(), "single article") || !strings.Contains(err.Error(), "previous") {
		t.Fatalf("single item failure = %v", err)
	}
}
