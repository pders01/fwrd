package web

import (
	"crypto/tls"
	"io"
	"net"
	"sync"
	"time"
)

// Classification is concurrent but bounded: idle clients must neither block
// Accept nor create an unbounded number of goroutines or open connections.
const (
	peekTimeout               = 10 * time.Second
	maxPendingClassifications = 64
)

// tlsMuxListener serves both HTTPS and cleartext HTTP on a single port. It
// peeks the first byte of each accepted connection: a TLS ClientHello begins
// with 0x16 (the handshake record type), so those connections are wrapped in a
// TLS server; anything else is returned as a plain connection, which the HTTP
// handler answers with a redirect to https.
//
// This keeps existing http:// bookmarks and the `fwrd net` bare-:80 cleartext
// redirect working once HTTPS becomes the default, without binding a second
// port. It relies on net/http special-casing *tls.Conn (it drives the
// handshake and sets r.TLS), so a plain connection surfaces as r.TLS == nil.
type tlsMuxListener struct {
	net.Listener
	cfg      *tls.Config
	ready    chan classifiedConn
	done     chan struct{}
	wg       sync.WaitGroup
	mu       sync.Mutex
	pending  map[net.Conn]struct{}
	err      error
	closeErr error
}

type classifiedConn struct {
	raw  net.Conn
	conn net.Conn
	err  error
}

func newTLSMux(ln net.Listener, cfg *tls.Config) net.Listener {
	m := &tlsMuxListener{
		Listener: ln, cfg: cfg, ready: make(chan classifiedConn),
		done: make(chan struct{}), pending: make(map[net.Conn]struct{}),
	}
	m.wg.Add(1)
	go m.classifyConnections()
	return m
}

func (m *tlsMuxListener) Accept() (net.Conn, error) {
	select {
	case result := <-m.ready:
		// Transfer ownership while holding the same lock used by shutdown.
		m.mu.Lock()
		delete(m.pending, result.raw)
		err := m.err
		m.mu.Unlock()
		if err != nil {
			if result.conn != nil {
				_ = result.conn.Close()
			}
			return nil, err
		}
		return result.conn, result.err
	case <-m.done:
		m.mu.Lock()
		defer m.mu.Unlock()
		return nil, m.err
	}
}

func (m *tlsMuxListener) classifyConnections() {
	defer m.wg.Done()
	for {
		c, err := m.Listener.Accept()
		if err != nil {
			// Preserve net/http's retry/backoff policy for temporary accept
			// errors without discarding already-classifying connections.
			if ne, ok := err.(interface{ Temporary() bool }); ok && ne.Temporary() {
				select {
				case m.ready <- classifiedConn{err: err}:
					continue
				case <-m.done:
					return
				}
			}
			m.stop(err)
			return
		}
		m.mu.Lock()
		if m.err != nil || len(m.pending) >= maxPendingClassifications {
			m.mu.Unlock()
			_ = c.Close()
			continue
		}
		m.pending[c] = struct{}{}
		m.wg.Add(1)
		m.mu.Unlock()
		go m.classify(c)
	}
}

func (m *tlsMuxListener) classify(c net.Conn) {
	defer m.wg.Done()
	pc, first, err := peekFirstByte(c, peekTimeout)
	if err == nil {
		var classified net.Conn = pc
		if first == 0x16 {
			classified = tls.Server(pc, m.cfg)
		}
		select {
		case m.ready <- classifiedConn{raw: c, conn: classified}:
			return // Accept now owns the connection and removes it from pending.
		case <-m.done:
		}
	}
	m.mu.Lock()
	delete(m.pending, c)
	m.mu.Unlock()
	_ = c.Close()
}

func (m *tlsMuxListener) stop(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return
	}
	m.err = err
	close(m.done)
	m.closeErr = m.Listener.Close()
	for c := range m.pending {
		_ = c.Close()
	}
}

func (m *tlsMuxListener) Close() error {
	m.stop(net.ErrClosed)
	// Includes the accept loop, so no classifier can start after this wait.
	m.wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closeErr
}

// peekedConn replays the single byte consumed during classification so the
// eventual reader (crypto/tls or net/http) sees the complete stream.
type peekedConn struct {
	net.Conn
	first    byte
	hasFirst bool
}

func (c *peekedConn) Read(p []byte) (int, error) {
	if c.hasFirst {
		if len(p) == 0 {
			return 0, nil
		}
		p[0] = c.first
		c.hasFirst = false
		return 1, nil
	}
	return c.Conn.Read(p)
}

func peekFirstByte(c net.Conn, timeout time.Duration) (*peekedConn, byte, error) {
	if timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return nil, 0, err
		}
	}
	var b [1]byte
	if _, err := io.ReadFull(c, b[:]); err != nil {
		return nil, 0, err
	}
	if timeout > 0 {
		// Clear the peek deadline so it doesn't bleed into the session;
		// http.Server applies its own read timeouts.
		if err := c.SetReadDeadline(time.Time{}); err != nil {
			return nil, 0, err
		}
	}
	return &peekedConn{Conn: c, first: b[0], hasFirst: true}, b[0], nil
}
