//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// endpointAddress uses a short, per-user directory below /tmp rather than the
// user's home directory. Unix-domain socket path limits are small on macOS,
// and a long home path can otherwise make an otherwise fixed-size name fail.
func endpointAddress(databaseID string) string {
	return filepath.Join(transportRunDir(), "store-"+databaseID[:20]+".sock")
}

func transportRunDir() string {
	return filepath.Join(string(filepath.Separator), "tmp", "fwrd-"+strconv.Itoa(os.Getuid()))
}

// DialEndpoint connects to the Unix-domain IPC endpoint. Callers apply
// handshake and operation deadlines to the returned connection.
func DialEndpoint(addr string) (net.Conn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
	defer cancel()
	return DialEndpointContext(ctx, addr)
}

// DialEndpointContext connects within the caller's time budget.
func DialEndpointContext(ctx context.Context, addr string) (net.Conn, error) {
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", addr)
	if err != nil {
		return nil, fmt.Errorf("dial unix %s: %w", addr, err)
	}
	return conn, nil
}

// ListenEndpoint binds a Unix-domain socket. The per-user run directory is
// owned by the current user and mode 0700; the socket itself is mode 0600.
func ListenEndpoint(endpoint Endpoint) (net.Listener, error) {
	dir := filepath.Dir(endpoint.Address)
	if err := ensurePrivateRunDir(dir); err != nil {
		return nil, err
	}

	if err := removeStaleSocket(endpoint); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", endpoint.Address)
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", endpoint.Address, err)
	}
	if err := os.Chmod(endpoint.Address, 0o600); err != nil {
		_ = ln.Close()
		_ = RemoveEndpoint(endpoint.Address)
		return nil, fmt.Errorf("chmod socket %s: %w", endpoint.Address, err)
	}
	return ln, nil
}

func ensurePrivateRunDir(dir string) error {
	fi, err := os.Lstat(dir)
	if errors.Is(err, os.ErrNotExist) {
		if mkdirErr := os.Mkdir(dir, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
			return fmt.Errorf("create run dir %s: %w", dir, mkdirErr)
		}
		fi, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("inspect run dir %s: %w", dir, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("run path %s is not a real directory", dir)
	}
	if stat, ok := fi.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("run directory %s is not owned by the current user", dir)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("chmod run dir %s: %w", dir, err)
	}
	return nil
}

// RemoveEndpoint removes only a Unix socket. It refuses to unlink a path that
// has been replaced by a regular file, directory, or symlink.
func RemoveEndpoint(addr string) error {
	fi, err := os.Lstat(addr)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a socket; refusing to remove", addr)
	}
	return os.Remove(addr)
}

// removeStaleSocket removes an unreachable socket only after validating its
// type and parent directory. A live matching or incompatible server is never
// unlinked.
func removeStaleSocket(endpoint Endpoint) error {
	fi, err := os.Lstat(endpoint.Address)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSocket == 0 || fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is not a socket; refusing to remove", endpoint.Address)
	}

	parent := filepath.Dir(endpoint.Address)
	if err := ensurePrivateRunDir(parent); err != nil {
		return err
	}

	probeErr := Probe(endpoint.Address, endpoint.DatabaseID)
	if probeErr == nil {
		return fmt.Errorf("another fwrd serve process is already running at %s", endpoint.Address)
	}
	if errors.Is(probeErr, ErrDatabaseMismatch) || errors.Is(probeErr, ErrProtocol) {
		return fmt.Errorf("live incompatible IPC server at %s: %w", endpoint.Address, probeErr)
	}

	if err := os.Remove(endpoint.Address); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", endpoint.Address, err)
	}
	return nil
}
