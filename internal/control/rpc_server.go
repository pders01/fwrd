package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

const maxRPCArticlePage = 1000

// RPCServer accepts IPC connections and dispatches storage and feed
// operation methods against the local store and manager.
type RPCServer struct {
	store       *storage.Store
	coordinator *MutationCoordinator
	databaseID  string
	ln          net.Listener
	operations  context.Context
	cancelOps   context.CancelFunc

	mu        sync.Mutex
	closed    bool
	conns     map[net.Conn]bool // true while dispatching an operation
	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

// NewRPCServer creates a server that listens on the given endpoint.
func NewRPCServer(endpoint ipc.Endpoint, store *storage.Store, coord *MutationCoordinator) (*RPCServer, error) {
	if store == nil || coord == nil {
		return nil, errors.New("RPC server requires a store and mutation coordinator")
	}
	ln, err := ipc.ListenEndpoint(endpoint)
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}

	operations, cancelOps := context.WithCancel(context.Background())
	return &RPCServer{
		store:       store,
		coordinator: coord,
		databaseID:  endpoint.DatabaseID,
		ln:          ln,
		operations:  operations,
		cancelOps:   cancelOps,
		conns:       make(map[net.Conn]bool),
	}, nil
}

// Serve accepts connections and handles them concurrently. It blocks
// until the listener is closed or an unrecoverable error occurs.
func (s *RPCServer) Serve() error {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept: %w", err)
		}

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			_ = conn.Close()
			continue
		}
		s.conns[conn] = false
		s.wg.Add(1)
		s.mu.Unlock()
		go s.handleConn(conn)
	}
}

// Shutdown stops accepting new clients, lets in-flight operations drain until
// ctx expires, then closes remaining connections. It always waits for handlers
// to leave before returning so callers may safely close the database.
func (s *RPCServer) Shutdown(ctx context.Context) error {
	s.stopAccepting()
	s.coordinator.BeginShutdown()
	s.closeIdleConnections()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return s.closeErr
	case <-ctx.Done():
		s.cancelOps()
		s.coordinator.CancelOperations()
		s.closeAllConnections()
		<-done
		if s.closeErr != nil {
			return errors.Join(ctx.Err(), s.closeErr)
		}
		return ctx.Err()
	}
}

// Close stops the server immediately and waits for all handlers.
func (s *RPCServer) Close() error {
	s.stopAccepting()
	s.coordinator.BeginShutdown()
	s.cancelOps()
	s.coordinator.CancelOperations()
	s.closeAllConnections()
	s.wg.Wait()
	return s.closeErr
}

func (s *RPCServer) stopAccepting() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.closeErr = s.ln.Close()
		if rmErr := ipc.RemoveEndpoint(s.ln.Addr().String()); rmErr != nil {
			s.closeErr = errors.Join(s.closeErr, rmErr)
		}
	})
}

func (s *RPCServer) closeIdleConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn, active := range s.conns {
		if !active {
			_ = conn.Close()
		}
	}
}

func (s *RPCServer) closeAllConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for conn := range s.conns {
		_ = conn.Close()
	}
}

func (s *RPCServer) beginOperation(conn net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	if _, ok := s.conns[conn]; !ok {
		return false
	}
	s.conns[conn] = true
	return true
}

func (s *RPCServer) endOperation(conn net.Conn) {
	s.mu.Lock()
	if _, ok := s.conns[conn]; ok {
		s.conns[conn] = false
	}
	closed := s.closed
	s.mu.Unlock()
	if closed {
		_ = conn.Close()
	}
}

// Addr returns the listener's address.
func (s *RPCServer) Addr() net.Addr {
	return s.ln.Addr()
}

func (s *RPCServer) handleConn(conn net.Conn) {
	defer func() {
		_ = conn.Close()
		s.mu.Lock()
		delete(s.conns, conn)
		s.mu.Unlock()
		s.wg.Done()
	}()

	// Bound clients that connect but never complete a handshake.
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	body, err := ipc.ReadFrame(conn)
	if err != nil {
		log.Printf("ipc handshake read: %v", err)
		return
	}

	var hs ipc.Handshake
	if err := json.Unmarshal(body, &hs); err != nil {
		log.Printf("ipc handshake unmarshal: %v", err)
		return
	}

	if hs.Version != ipc.ProtocolVersion {
		s.writeHandshake(conn, hs.Version, ipc.CodeProtocol, "protocol version mismatch")
		return
	}

	if hs.DatabaseID != s.databaseID {
		s.writeHandshake(conn, hs.Version, ipc.CodeMismatch, "database mismatch")
		return
	}

	// Send successful handshake response.
	respHS := ipc.Handshake{
		Version:    ipc.ProtocolVersion,
		DatabaseID: s.databaseID,
		Client:     ipc.ClientIdent(),
	}
	if err := ipc.WriteFrame(conn, respHS); err != nil {
		log.Printf("ipc handshake write: %v", err)
		return
	}
	_ = conn.SetDeadline(time.Time{})

	// Dispatch loop. The read deadline bounds peers that send only part of a
	// frame; it is cleared while an operation itself is executing.
	for {
		_ = conn.SetReadDeadline(time.Now().Add(30 * time.Second))
		body, err := ipc.ReadFrame(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, ipc.ErrConnClosed) {
				log.Printf("ipc read: %v", err)
			}
			return
		}

		var req ipc.Request
		if err := json.Unmarshal(body, &req); err != nil {
			s.writeError(conn, 0, ipc.CodeProtocol, "invalid request JSON")
			return
		}

		_ = conn.SetReadDeadline(time.Time{})
		if req.Version != ipc.ProtocolVersion {
			s.writeError(conn, req.ID, ipc.CodeProtocol, "protocol version mismatch")
			continue
		}

		if !s.beginOperation(conn) {
			return
		}
		s.safeDispatch(conn, &req)
		s.endOperation(conn)
	}
}

func (s *RPCServer) writeHandshake(conn net.Conn, _ int, code, msg string) {
	resp := ipc.Handshake{
		Version:    ipc.ProtocolVersion,
		DatabaseID: s.databaseID,
		Client:     ipc.ClientIdent(),
		Error:      &ipc.ErrorBody{Code: code, Message: msg},
	}
	_ = ipc.WriteFrame(conn, resp)
}

func (s *RPCServer) writeError(conn net.Conn, id uint64, code, msg string) {
	resp := ipc.Response{
		Version: ipc.ProtocolVersion,
		ID:      id,
		Error:   &ipc.ErrorBody{Code: code, Message: msg},
	}
	_ = ipc.WriteFrame(conn, resp)
}

func (s *RPCServer) writeResult(conn net.Conn, id uint64, result any) {
	var raw json.RawMessage
	if result != nil {
		data, err := json.Marshal(result)
		if err != nil {
			s.writeError(conn, id, ipc.CodeInternal, fmt.Sprintf("marshal result: %v", err))
			return
		}
		raw = data
	}
	resp := ipc.Response{
		Version: ipc.ProtocolVersion,
		ID:      id,
		Result:  raw,
	}
	encoded, err := json.Marshal(resp)
	if err != nil {
		s.writeError(conn, id, ipc.CodeInternal, "marshal response")
		return
	}
	if len(encoded) > ipc.MaxFrameSize {
		s.writeError(conn, id, ipc.CodeResultTooLarge, "result is too large; use pagination")
		return
	}
	if err := ipc.WriteFrame(conn, resp); err != nil {
		_ = conn.Close()
	}
}

func (s *RPCServer) safeDispatch(conn net.Conn, req *ipc.Request) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.Printf("ipc dispatch panic in %q: %v", req.Method, recovered)
			s.writeError(conn, req.ID, ipc.CodeInternal, "internal server error")
		}
	}()
	if req.ID == 0 || strings.TrimSpace(req.Method) == "" {
		s.writeError(conn, req.ID, ipc.CodeInvalidArg, "request id and method are required")
		return
	}
	s.dispatch(conn, req)
}

func (s *RPCServer) dispatch(conn net.Conn, req *ipc.Request) {
	switch req.Method {
	// Storage methods.
	case "SaveFeed":
		var feed storage.Feed
		if err := s.decodeParams(req, &feed); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := validateFeed(&feed); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := s.coordinator.SaveFeed(s.operations, &feed); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	case "GetFeed":
		dispatchIDResult(s, conn, req, s.store.GetFeed)

	case "GetAllFeeds":
		feeds, err := s.store.GetAllFeeds()
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, feeds)

	case "SaveArticles":
		var articles []*storage.Article
		if err := s.decodeParams(req, &articles); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := validateArticles(articles); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := s.coordinator.SaveArticles(s.operations, articles); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	case "GetArticle":
		dispatchIDResult(s, conn, req, s.store.GetArticle)

	case "GetArticlesWithCursor":
		var p struct {
			FeedID string `json:"feed_id"`
			Limit  int    `json:"limit"`
			Cursor string `json:"cursor"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if p.Limit <= 0 || p.Limit > maxRPCArticlePage {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, fmt.Sprintf("limit must be between 1 and %d", maxRPCArticlePage))
			return
		}
		articles, err := s.store.GetArticlesWithCursor(p.FeedID, p.Limit, p.Cursor)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, articles)

	case "FeedStats":
		stats, err := s.store.FeedStats()
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, stats)

	case "MarkArticleRead":
		s.dispatchArticleMutation(conn, req, "read", s.store.MarkArticleRead)

	case "MarkArticleStarred":
		s.dispatchArticleMutation(conn, req, "starred", s.store.MarkArticleStarred)

	case "DeleteFeed":
		var p struct{ ID string }
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := requireValue("id", p.ID); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := s.coordinator.DeleteFeed(s.operations, p.ID); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	// Feed operation methods — all pass through the shared coordinator.
	case "AddFeed":
		var p struct{ URL string }
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := requireValue("url", p.URL); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		feedResult, err := s.coordinator.AddFeed(s.operations, p.URL)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, feedResult)

	case "RefreshFeed":
		var p struct {
			ID    string `json:"id"`
			Force bool   `json:"force"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := requireValue("id", p.ID); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := s.coordinator.RefreshFeed(s.operations, p.ID, p.Force); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	case "RefreshAllFeeds":
		var p struct{ Force bool }
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		summary, err := s.coordinator.RefreshAllFeeds(s.operations, p.Force)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, summary)

	case "ImportFeeds":
		var p struct{ URLs []string }
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		for i, url := range p.URLs {
			if err := requireValue(fmt.Sprintf("urls[%d]", i), url); err != nil {
				s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
				return
			}
		}
		summary, err := s.coordinator.ImportFeeds(s.operations, p.URLs)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, summary)

	default:
		s.writeError(conn, req.ID, ipc.CodeUnknownMethod, fmt.Sprintf("unknown method: %s", req.Method))
	}
}

func dispatchIDResult[T any](s *RPCServer, conn net.Conn, req *ipc.Request, getter func(string) (T, error)) {
	var params struct{ ID string }
	if err := s.decodeParams(req, &params); err != nil {
		s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
		return
	}
	if err := requireValue("id", params.ID); err != nil {
		s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
		return
	}
	result, err := getter(params.ID)
	if err != nil {
		s.writeError(conn, req.ID, errorCode(err), err.Error())
		return
	}
	s.writeResult(conn, req.ID, result)
}

func (s *RPCServer) dispatchArticleMutation(conn net.Conn, req *ipc.Request, field string, mutate func(string, bool) error) {
	var params struct {
		ID      string `json:"id"`
		Read    bool   `json:"read"`
		Starred bool   `json:"starred"`
	}
	if err := s.decodeParams(req, &params); err != nil {
		s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
		return
	}
	if err := requireValue("id", params.ID); err != nil {
		s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
		return
	}
	value := params.Read
	if field == "starred" {
		value = params.Starred
	}
	if err := mutate(params.ID, value); err != nil {
		s.writeError(conn, req.ID, errorCode(err), err.Error())
		return
	}
	s.writeResult(conn, req.ID, nil)
}

func requireValue(name, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}

func validateFeed(value *storage.Feed) error {
	if value == nil {
		return errors.New("feed is required")
	}
	if err := requireValue("feed.id", value.ID); err != nil {
		return err
	}
	return requireValue("feed.url", value.URL)
}

func validateArticles(articles []*storage.Article) error {
	for i, article := range articles {
		if article == nil {
			return fmt.Errorf("articles[%d] must not be null", i)
		}
		if err := requireValue(fmt.Sprintf("articles[%d].id", i), article.ID); err != nil {
			return err
		}
		if err := requireValue(fmt.Sprintf("articles[%d].feed_id", i), article.FeedID); err != nil {
			return err
		}
	}
	return nil
}

func (s *RPCServer) decodeParams(req *ipc.Request, v any) error {
	if req.Params == nil {
		return errors.New("params are required")
	}
	// req.Params is any, but the client sends JSON objects or arrays.
	// Re-marshal to get consistent decoding.
	data, err := json.Marshal(req.Params)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// errorCode maps common errors to stable IPC error codes.
func errorCode(err error) string {
	if err == nil {
		return ipc.CodeOK
	}
	if errors.Is(err, storage.ErrDatabaseLocked) {
		return ipc.CodeLocked
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ipc.CodeUnavailable
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return ipc.CodeNotFound
	}
	return ipc.CodeInternal
}
