package web

import (
	"crypto/tls"
	"io"
	"net"
	"time"
)

// peekTimeout bounds how long Accept waits for the first byte before giving up
// on a connection, so a client that connects but never sends can't wedge the
// accept loop. The real per-request timeouts on http.Server take over once a
// connection is classified.
const peekTimeout = 10 * time.Second

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
	cfg *tls.Config
}

func newTLSMux(ln net.Listener, cfg *tls.Config) net.Listener {
	return &tlsMuxListener{Listener: ln, cfg: cfg}
}

func (m *tlsMuxListener) Accept() (net.Conn, error) {
	c, err := m.Listener.Accept()
	if err != nil {
		return nil, err
	}
	pc, first, perr := peekFirstByte(c, peekTimeout)
	if perr != nil {
		_ = c.Close()
		// A dead or idle connection is per-connection noise, not a listener
		// failure — drop it and wait for the next rather than killing Serve.
		return m.Accept()
	}
	if first == 0x16 { // TLS handshake record
		return tls.Server(pc, m.cfg), nil
	}
	return pc, nil
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
		n, err := c.Conn.Read(p[1:])
		return n + 1, err
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
