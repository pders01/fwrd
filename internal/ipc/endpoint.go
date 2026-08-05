package ipc

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// Endpoint derives the local IPC endpoint for a database path.
type Endpoint struct {
	DatabaseID string // full SHA-256 hex of the canonical path
	Address    string // transport-specific dial/listen address
}

// NewEndpoint derives the database identity and IPC endpoint from a
// validated, canonical database path. The path must already be absolute
// and cleaned.
func NewEndpoint(canonicalDBPath string) Endpoint {
	sum := sha256.Sum256([]byte(canonicalDBPath))
	id := fmt.Sprintf("%x", sum)

	return Endpoint{DatabaseID: id, Address: endpointAddress(id)}
}

// CanonicalDBPath cleans, absolutizes, and resolves symlinks for a
// database path, returning the canonical form used for the database ID.
func CanonicalDBPath(dbPath string) (string, error) {
	cleaned := filepath.Clean(dbPath)

	if !filepath.IsAbs(cleaned) {
		abs, err := filepath.Abs(cleaned)
		if err != nil {
			return "", fmt.Errorf("abs: %w", err)
		}
		cleaned = abs
	}

	// Resolve symlinks for existing files, or resolve up to the nearest
	// existing parent for new files.
	resolved, err := resolveExisting(cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve: %w", err)
	}

	return normalizeCanonicalPath(resolved, runtime.GOOS == "windows"), nil
}

func normalizeCanonicalPath(path string, windows bool) string {
	path = filepath.Clean(path)
	// Windows path lookup is case-insensitive. Normalize identity so aliases
	// that differ only in drive-letter or component case share one endpoint.
	if windows {
		path = strings.ToLower(path)
	}
	return path
}

// resolveExisting resolves symlinks in path. For a non-existent leaf it
// resolves the nearest existing parent and appends the remaining
// basename.
func resolveExisting(p string) (string, error) {
	_, statErr := os.Lstat(p)
	if statErr == nil {
		return filepath.EvalSymlinks(p)
	}
	if !os.IsNotExist(statErr) {
		return "", statErr
	}
	// Walk up to the nearest existing ancestor.
	parent := filepath.Dir(p)
	if parent == p {
		// At root, nothing more to resolve.
		return p, nil
	}
	resolvedParent, err := resolveExisting(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(p)), nil
}

// RunDir returns the transport-specific directory that holds IPC sockets.
// It is empty on transports, such as Windows named pipes, that do not use one.
func RunDir() string {
	return transportRunDir()
}

// ShortID returns the first 8 hex chars of the database ID for log
// display.
func (e Endpoint) ShortID() string {
	if len(e.DatabaseID) < 8 {
		return e.DatabaseID
	}
	return e.DatabaseID[:8]
}

// FWRDVersion returns the client identification string for handshakes.
// The caller should set the actual version at link time.
var FWRDVersion = "dev"

// ClientIdent builds the "fwrd/version" string sent in the handshake.
func ClientIdent() string {
	return "fwrd/" + FWRDVersion
}

// IsDatabaseMismatch reports whether the error indicates a handshake
// failed because the server's database ID differs from the client's.
func IsDatabaseMismatch(err error) bool {
	return errors.Is(err, ErrDatabaseMismatch)
}
