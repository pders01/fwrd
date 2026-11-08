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

const (
	maxRPCArticlePage = 1000
	maxRPCConnections = 32
	maxRPCDispatches  = 4
	maxRPCFrameBytes  = 128 << 20
	rpcWriteTimeout   = 2 * time.Second
)

// RPCServer accepts IPC connections and dispatches storage and feed
// operation methods against the local store and manager.
type RPCServer struct {
	store         *storage.Store
	coordinator   *MutationCoordinator
	databaseID    string
	ln            net.Listener
	operations    context.Context
	cancelOps     context.CancelFunc
	frameBudget   *ipc.FrameBudget
	dispatchSlots chan struct{}
	writeTimeout  time.Duration

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
		store:         store,
		coordinator:   coord,
		databaseID:    endpoint.DatabaseID,
		ln:            ln,
		operations:    operations,
		cancelOps:     cancelOps,
		frameBudget:   ipc.NewFrameBudget(maxRPCFrameBytes),
		dispatchSlots: make(chan struct{}, maxRPCDispatches),
		writeTimeout:  rpcWriteTimeout,
		conns:         make(map[net.Conn]bool),
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
		if s.closed || len(s.conns) >= maxRPCConnections {
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
		// The listener owns endpoint cleanup. An additional unlink here could
		// remove a new listener that bound the same path after Close.

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
	body, release, err := ipc.ReadFrameWithBudget(conn, 16<<10, s.frameBudget)
	if err != nil {
		log.Printf("ipc handshake read: %v", err)
		return
	}

	var hs ipc.Handshake
	err = json.Unmarshal(body, &hs)
	release()
	if err != nil {
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

	// Keep a reader active during dispatch: EOF cancels queued/running work,
	// including legacy clients without a deadline. There is at most one queued
	// frame per connection, charged to the shared budget until dispatch ends.
	// Excess pipelining is rejected rather than blocking the disconnect reader.
	ctx, cancel := context.WithCancel(s.operations)
	requests := make(chan rpcRequest, 1)
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer cancel()
		defer close(requests)
		s.readRequests(ctx, conn, requests)
	}()
	defer func() {
		cancel()
		_ = conn.Close()
		<-readerDone
		for pending := range requests {
			pending.release()
		}
	}()
	for request := range requests {
		s.handleRequest(ctx, conn, &request)
		if ctx.Err() != nil {
			return
		}
	}
}

type rpcRequest struct {
	req     ipc.Request
	release func()
}

func (s *RPCServer) readRequests(ctx context.Context, conn net.Conn, requests chan<- rpcRequest) {
	for {
		body, release, err := ipc.ReadRequestFrameWithBudget(conn, 30*time.Second, s.frameBudget)
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, ipc.ErrConnClosed) && ctx.Err() == nil {
				log.Printf("ipc read: %v", err)
			}
			return
		}
		// Avoid decoding params into maps of interface values and re-marshalling
		// them; retain raw JSON until the selected method knows its schema.
		var wire struct {
			Version  int             `json:"version"`
			ID       uint64          `json:"id"`
			Method   string          `json:"method"`
			Params   json.RawMessage `json:"params"`
			Deadline time.Time       `json:"deadline"`
		}
		if err := json.Unmarshal(body, &wire); err != nil {
			release()
			// Only the handler writes responses; never race an active result.
			return
		}
		request := rpcRequest{req: ipc.Request{Version: wire.Version, ID: wire.ID, Method: wire.Method, Params: wire.Params, Deadline: wire.Deadline}, release: release}
		select {
		case requests <- request:
		case <-ctx.Done():
			release()
			return
		default:
			// The protocol permits one in-flight call per connection. Keep one
			// pending frame for scheduling races, but reject excess pipelining.
			release()
			return
		}
	}
}

func (s *RPCServer) handleRequest(parent context.Context, conn net.Conn, request *rpcRequest) {
	defer request.release()
	req := &request.req
	ctx, cancel := context.WithCancel(parent)
	if !req.Deadline.IsZero() {
		cancel()
		ctx, cancel = context.WithDeadline(parent, req.Deadline)
	}
	defer cancel()
	if req.Version != ipc.ProtocolVersion {
		s.writeError(conn, req.ID, ipc.CodeProtocol, "protocol version mismatch")
		return
	}
	if !s.beginOperation(conn) {
		return
	}
	defer s.endOperation(conn)
	select {
	case s.dispatchSlots <- struct{}{}:
		defer func() { <-s.dispatchSlots }()
	case <-ctx.Done():
		return
	}
	if ctx.Err() != nil {
		return
	}
	s.safeDispatch(ctx, conn, req)
}

func (s *RPCServer) writeHandshake(conn net.Conn, _ int, code, msg string) {
	resp := ipc.Handshake{
		Version:    ipc.ProtocolVersion,
		DatabaseID: s.databaseID,
		Client:     ipc.ClientIdent(),
		Error:      &ipc.ErrorBody{Code: code, Message: msg},
	}
	s.writeResponse(conn, resp)
}

func (s *RPCServer) writeResponse(conn net.Conn, response any) {
	timeout := s.writeTimeout
	if timeout <= 0 {
		timeout = rpcWriteTimeout
	}
	if err := conn.SetWriteDeadline(time.Now().Add(timeout)); err != nil {
		_ = conn.Close()
		return
	}
	if err := ipc.WriteFrame(conn, response); err != nil {
		_ = conn.Close()
	}
}

func (s *RPCServer) writeError(conn net.Conn, id uint64, code, msg string) {
	resp := ipc.Response{
		Version: ipc.ProtocolVersion,
		ID:      id,
		Error:   &ipc.ErrorBody{Code: code, Message: msg},
	}
	s.writeResponse(conn, resp)
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
	s.writeResponse(conn, resp)
}

func (s *RPCServer) safeDispatch(ctx context.Context, conn net.Conn, req *ipc.Request) {
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
	s.dispatch(ctx, conn, req)
}

func (s *RPCServer) dispatch(ctx context.Context, conn net.Conn, req *ipc.Request) {
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
		if err := s.coordinator.SaveFeed(ctx, &feed); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	case "CommitFeed", "CommitFeedCounted":
		var p struct {
			Feed       *storage.Feed      `json:"feed"`
			Articles   []*storage.Article `json:"articles"`
			CreateOnly bool               `json:"create_only"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := validateFeed(p.Feed); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := validateArticles(p.Articles); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if req.Method == "CommitFeedCounted" {
			result, err := s.coordinator.CommitFeedCounted(ctx, p.Feed, p.Articles, p.CreateOnly)
			if err != nil {
				s.writeError(conn, req.ID, errorCode(err), err.Error())
				return
			}
			s.writeResult(conn, req.ID, result)
			return
		}
		saved, err := s.coordinator.CommitFeed(ctx, p.Feed, p.Articles, p.CreateOnly)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, saved)

	case "SetFeedError":
		var p struct {
			ID      string    `json:"id"`
			Message string    `json:"message"`
			At      time.Time `json:"at"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := requireValue("id", p.ID); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		value, err := s.coordinator.SetFeedError(ctx, p.ID, p.Message, p.At)
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, value)

	case "SetFeedDesk", "RenameFeed":
		var p struct {
			ID     string `json:"id"`
			DeskID string `json:"desk_id"`
			Title  string `json:"title"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := requireValue("id", p.ID); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		var value *storage.Feed
		var err error
		if req.Method == "SetFeedDesk" {
			value, err = s.coordinator.SetFeedDesk(ctx, p.ID, p.DeskID)
		} else {
			if titleErr := requireValue("title", p.Title); titleErr != nil {
				s.writeError(conn, req.ID, ipc.CodeInvalidArg, titleErr.Error())
				return
			}
			value, err = s.coordinator.RenameFeed(ctx, p.ID, p.Title)
		}
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, value)

	case "GetFeed":
		dispatchIDResult(s, conn, req, s.store.GetFeed)

	case "GetAllFeeds":
		feeds, err := s.store.GetAllFeeds()
		if err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, feeds)

	case "SaveArticles", "MergeArticles":
		var articles []*storage.Article
		if err := s.decodeParams(req, &articles); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if err := validateArticles(articles); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if req.Method == "MergeArticles" {
			saved, err := s.coordinator.MergeArticles(ctx, articles)
			if err != nil {
				s.writeError(conn, req.ID, errorCode(err), err.Error())
				return
			}
			s.writeResult(conn, req.ID, saved)
			return
		}
		if err := s.coordinator.SaveArticles(ctx, articles); err != nil {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		s.writeResult(conn, req.ID, nil)

	case "GetArticle":
		dispatchIDResult(s, conn, req, s.store.GetArticle)

	case "GetArticlesWithCursor":
		var p struct {
			FeedID   string `json:"feed_id"`
			Limit    int    `json:"limit"`
			Cursor   string `json:"cursor"`
			MaxBytes int    `json:"max_bytes"`
		}
		if err := s.decodeParams(req, &p); err != nil {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, err.Error())
			return
		}
		if p.Limit <= 0 || p.Limit > maxRPCArticlePage {
			s.writeError(conn, req.ID, ipc.CodeInvalidArg, fmt.Sprintf("limit must be between 1 and %d", maxRPCArticlePage))
			return
		}
		maxBytes := ipc.MaxFrameSize - 1024 // envelope overhead
		if p.MaxBytes > 0 {
			maxBytes = min(maxBytes, p.MaxBytes)
		}
		articles, err := s.store.GetArticlesWithCursorBounded(p.FeedID, p.Limit, p.Cursor, maxBytes)
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
		if err := s.coordinator.DeleteFeed(ctx, p.ID); err != nil {
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
		feedResult, err := s.coordinator.AddFeed(ctx, p.URL)
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
		if err := s.coordinator.RefreshFeed(ctx, p.ID, p.Force); err != nil {
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
		summary, err := s.coordinator.RefreshAllFeeds(ctx, p.Force)
		// A per-feed timeout can wrap context.DeadlineExceeded without the
		// request itself expiring. Only the request context decides whether
		// cancellation takes precedence over a completed partial result.
		if ctxErr := ctx.Err(); ctxErr != nil {
			s.writeError(conn, req.ID, errorCode(ctxErr), ctxErr.Error())
			return
		}
		// Only errors represented by the summary can travel as a result.
		// Keep an unrelated top-level failure even if some feeds completed.
		if err != nil && !summary.OnlyFeedErrors(err) {
			s.writeError(conn, req.ID, errorCode(err), err.Error())
			return
		}
		// Completed per-feed failures belong in the result, not the RPC
		// error envelope, which cannot carry a result at the same time.
		s.writeResult(conn, req.ID, encodeRefreshSummary(summary))

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
		summary, err := s.coordinator.ImportFeeds(ctx, p.URLs)
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
	if raw, ok := req.Params.(json.RawMessage); ok {
		if len(raw) == 0 || string(raw) == "null" {
			return errors.New("params are required")
		}
		return json.Unmarshal(raw, v)
	}
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
	if errors.Is(err, storage.ErrResultTooLarge) {
		return ipc.CodeResultTooLarge
	}
	if errors.Is(err, storage.ErrFeedExists) {
		return ipc.CodeFeedExists
	}
	if errors.Is(err, storage.ErrArticleFeedChanged) {
		return ipc.CodeArticleFeedChanged
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
