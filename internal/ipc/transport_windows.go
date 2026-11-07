//go:build windows

package ipc

import (
	"context"
	"fmt"
	"net"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func endpointAddress(databaseID string) string {
	return `\\.\pipe\fwrd-store-` + databaseID
}

func transportRunDir() string { return "" }

// DialEndpoint connects to the named pipe at addr.
func DialEndpoint(addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	return DialEndpointContext(ctx, addr)
}

// DialEndpointContext connects within the caller's time budget.
func DialEndpointContext(ctx context.Context, addr string) (net.Conn, error) {
	conn, err := winio.DialPipeContext(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("dial pipe %s: %w", addr, err)
	}
	return conn, nil
}

// ListenEndpoint creates a named-pipe listener whose ACL grants full access
// only to the current user and LocalSystem.
func ListenEndpoint(endpoint Endpoint) (net.Listener, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	sid := user.User.Sid.String()
	cfg := &winio.PipeConfig{
		SecurityDescriptor: fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;SY)", sid),
	}
	ln, err := winio.ListenPipe(endpoint.Address, cfg)
	if err != nil {
		return nil, fmt.Errorf("listen pipe %s: %w", endpoint.Address, err)
	}
	return ln, nil
}

// RemoveEndpoint is a no-op on Windows; named pipes disappear when the last
// handle is closed.
func RemoveEndpoint(string) error { return nil }
