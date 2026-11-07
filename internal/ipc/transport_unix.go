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
	"sync"
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

// Unix network operations already report net.OpError or standard EOF/closed
// sentinels. There are no additional native pipe errors to classify here.
func retryableTransportError(error) bool { return false }

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

	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: endpoint.Address, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen unix %s: %w", endpoint.Address, err)
	}
	// net.UnixListener's default unlink-on-close does not establish that the
	// pathname still identifies our socket. Retain its inode for safe cleanup.
	ln.SetUnlinkOnClose(false)
	info, err := os.Lstat(endpoint.Address)
	if err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("inspect bound socket: %w", err)
	}
	owned := &ownedUnixListener{UnixListener: ln, path: endpoint.Address, info: info}
	if chmodErr := os.Chmod(endpoint.Address, 0o600); chmodErr != nil {
		_ = owned.Close()
		return nil, fmt.Errorf("chmod socket %s: %w", endpoint.Address, chmodErr)
	}
	return owned, nil
}

// Closing our own listener is positive stale evidence, but only for the exact
// socket we bound. Never unlink a replacement installed by another process.
type ownedUnixListener struct {
	*net.UnixListener
	path string
	info os.FileInfo
	once sync.Once
	err  error
}

func (ln *ownedUnixListener) Close() error {
	ln.once.Do(func() {
		ln.err = ln.UnixListener.Close()
		current, err := os.Lstat(ln.path)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err != nil {
			ln.err = errors.Join(ln.err, err)
			return
		}
		if os.SameFile(ln.info, current) {
			ln.err = errors.Join(ln.err, os.Remove(ln.path))
		}
	})
	return ln.err
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

// RemoveEndpoint removes only positively stale Unix sockets. Live or ambiguous
// endpoints, regular files, directories, symlinks and replacements are retained.
func RemoveEndpoint(addr string) error {
	return removeStaleSocket(Endpoint{Address: addr})
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
	if dirErr := ensurePrivateRunDir(parent); dirErr != nil {
		return dirErr
	}

	probeErr := Probe(endpoint.Address, endpoint.DatabaseID)
	if probeErr == nil {
		return fmt.Errorf("another fwrd serve process is already running at %s", endpoint.Address)
	}
	if errors.Is(probeErr, ErrDatabaseMismatch) || errors.Is(probeErr, ErrProtocol) {
		return fmt.Errorf("live incompatible IPC server at %s: %w", endpoint.Address, probeErr)
	}

	// A timeout, EOF, malformed handshake, or permission failure is ambiguous:
	// the listener may be alive but busy or speaking a different protocol.
	// Only connection refusal is positive evidence of a stale Unix socket.
	if !errors.Is(probeErr, syscall.ECONNREFUSED) {
		return fmt.Errorf("cannot establish stale IPC socket at %s; refusing to remove: %w", endpoint.Address, probeErr)
	}
	current, err := os.Lstat(endpoint.Address)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(fi, current) {
		return fmt.Errorf("IPC socket changed during probe; refusing to remove")
	}
	if err := os.Remove(endpoint.Address); err != nil {
		return fmt.Errorf("remove stale socket %s: %w", endpoint.Address, err)
	}
	return nil
}
