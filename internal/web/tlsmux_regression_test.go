package web

import (
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

type queuedListener struct {
	connections chan net.Conn
	done        chan struct{}
	once        sync.Once
}

func (l *queuedListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.connections:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *queuedListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *queuedListener) Addr() net.Addr { return &net.TCPAddr{} }

func testQueuedMux(t *testing.T) (*queuedListener, net.Listener) {
	t.Helper()
	l := &queuedListener{connections: make(chan net.Conn, maxPendingClassifications+10), done: make(chan struct{})}
	m := newTLSMux(l, testTLSConfig(t))
	t.Cleanup(func() { _ = m.Close() })
	return l, m
}

func TestTLSMuxIdleClientDoesNotStarveAccept(t *testing.T) {
	l, m := testQueuedMux(t)
	idle, idlePeer := net.Pipe()
	defer idlePeer.Close()
	l.connections <- idle
	active, peer := net.Pipe()
	defer peer.Close()
	l.connections <- active
	go func() { _, _ = peer.Write([]byte("G")) }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := m.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	select {
	case c := <-accepted:
		defer c.Close()
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, err := io.ReadFull(c, b[:]); err != nil || b[0] != 'G' {
			t.Fatalf("classified connection lost first byte: %q, %v", b, err)
		}
	case <-time.After(time.Second):
		t.Fatal("idle first connection blocked active connection")
	}
}

type closeObservedConn struct {
	net.Conn
	closed chan struct{}
	once   sync.Once
}

func (c *closeObservedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func TestTLSMuxDropsRepeatedDeadConnections(t *testing.T) {
	l, m := testQueuedMux(t)
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := m.Accept()
		if err == nil {
			accepted <- c
		}
	}()
	for range 512 {
		c, peer := net.Pipe()
		_ = peer.Close()
		observed := &closeObservedConn{Conn: c, closed: make(chan struct{})}
		l.connections <- observed
		select {
		case <-observed.closed:
		case <-time.After(time.Second):
			t.Fatal("dead connection did not release its classifier slot")
		}
	}
	c, peer := net.Pipe()
	defer peer.Close()
	l.connections <- c
	go func() { _, _ = peer.Write([]byte("G")) }()
	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(time.Second):
		t.Fatal("dead connection retries terminated or starved Accept")
	}
}

type observedReadConn struct {
	net.Conn
	reading chan<- struct{}
}

func (c *observedReadConn) Read(p []byte) (int, error) {
	c.reading <- struct{}{}
	return c.Conn.Read(p)
}

func TestTLSMuxBoundsPendingAndClosesAllOnShutdown(t *testing.T) {
	l, m := testQueuedMux(t)
	reading := make(chan struct{}, maxPendingClassifications)
	peers := make([]net.Conn, 0, maxPendingClassifications)
	defer func() {
		for _, peer := range peers {
			_ = peer.Close()
		}
	}()
	for range maxPendingClassifications {
		c, peer := net.Pipe()
		peers = append(peers, peer)
		l.connections <- &observedReadConn{Conn: c, reading: reading}
	}
	for range maxPendingClassifications {
		select {
		case <-reading:
		case <-time.After(time.Second):
			t.Fatal("classifiers did not start concurrently")
		}
	}
	// A saturated listener drops excess sockets instead of starting more
	// workers or blocking the underlying accept loop indefinitely.
	extra, extraPeer := net.Pipe()
	defer extraPeer.Close()
	l.connections <- extra
	_ = extraPeer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := extraPeer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("excess connection was not closed: %v", err)
	}
	closed := make(chan error, 1)
	go func() { closed <- m.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown waited for classification deadlines")
	}
	for _, peer := range peers {
		_ = peer.SetReadDeadline(time.Now().Add(time.Second))
		if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatalf("shutdown left unaccepted connection open: %v", err)
		}
	}
	if _, err := m.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Accept after Close: %v", err)
	}
}

func TestTLSMuxCloseWithoutAcceptClosesReadyConnection(t *testing.T) {
	l, m := testQueuedMux(t)
	c, peer := net.Pipe()
	defer peer.Close()
	l.connections <- c
	_ = peer.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := peer.Write([]byte("G")); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("classified but undelivered connection leaked: %v", err)
	}
}

type temporaryAcceptError struct{}

func (temporaryAcceptError) Error() string   { return "temporary accept failure" }
func (temporaryAcceptError) Temporary() bool { return true }

type temporaryListener struct {
	net.Listener
	failed bool
}

func (l *temporaryListener) Accept() (net.Conn, error) {
	if !l.failed {
		l.failed = true
		return nil, temporaryAcceptError{}
	}
	return l.Listener.Accept()
}

func TestTLSMuxPreservesTemporaryAcceptErrors(t *testing.T) {
	l := &queuedListener{connections: make(chan net.Conn, 1), done: make(chan struct{})}
	m := newTLSMux(&temporaryListener{Listener: l}, testTLSConfig(t))
	defer m.Close()
	if _, err := m.Accept(); !errors.As(err, new(temporaryAcceptError)) {
		t.Fatalf("temporary listener failure not returned to HTTP server: %v", err)
	}
	c, peer := net.Pipe()
	defer peer.Close()
	l.connections <- c
	go func() { _, _ = peer.Write([]byte("G")) }()
	accepted, err := m.Accept()
	if err != nil {
		t.Fatalf("temporary error permanently closed listener: %v", err)
	}
	_ = accepted.Close()
}

func TestTLSMuxListenerFailureClosesPendingConnections(t *testing.T) {
	l, m := testQueuedMux(t)
	reading := make(chan struct{}, 1)
	c, peer := net.Pipe()
	defer peer.Close()
	l.connections <- &observedReadConn{Conn: c, reading: reading}
	select {
	case <-reading:
	case <-time.After(time.Second):
		t.Fatal("classification did not start")
	}
	_ = l.Close()
	if _, err := m.Accept(); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("listener error not propagated: %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	_ = peer.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := peer.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("listener failure leaked classifier connection: %v", err)
	}
}
