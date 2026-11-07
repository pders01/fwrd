package control

import (
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

// Session bundles storage access and feed lifecycle operations so CLI
// commands can use them without branching on local versus remote access.
type Session struct {
	Store *storage.Store
	Feeds FeedOperations

	client  *ipc.Client // non-nil when using IPC
	closers []io.Closer // local listeners/indexes, closed before Store
}

type sessionOptions struct {
	timeout       time.Duration
	canonicalPath bool
	localSetup    func(*storage.Store, *feed.Manager) (io.Closer, error)
}

// SessionOption configures command-session creation.
type SessionOption func(*sessionOptions)

// WithOpenTimeout configures the local bbolt lock timeout.
func WithOpenTimeout(timeout time.Duration) SessionOption {
	return func(options *sessionOptions) { options.timeout = timeout }
}

// WithCanonicalDBPath declares that dbPath was already canonicalized and
// security-validated by the command path. It prevents resolving a mutable
// symlink a second time between validation and opening bbolt.
func WithCanonicalDBPath() SessionOption {
	return func(options *sessionOptions) { options.canonicalPath = true }
}

// WithLocalSetup completes local manager setup before OpenSession returns.
// It is not called for remote sessions. Any returned closer is owned by the
// session and is closed before the local Store.
func WithLocalSetup(setup func(*storage.Store, *feed.Manager) (io.Closer, error)) SessionOption {
	return func(options *sessionOptions) { options.localSetup = setup }
}

// Close releases session-owned listeners/indexes before the Store.
func (s *Session) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	for i := len(s.closers) - 1; i >= 0; i-- {
		closeErr = errors.Join(closeErr, s.closers[i].Close())
	}
	if s.Store != nil {
		return errors.Join(closeErr, s.Store.Close())
	}
	if s.client != nil {
		return errors.Join(closeErr, s.client.Close())
	}
	return closeErr
}

// OpenSession resolves how to access the database and constructs the
// appropriate Store and FeedOperations. When a matching serve process is
// running, it returns an IPC-backed session; otherwise it opens bbolt directly.
func OpenSession(dbPath string, cfg *config.Config, options ...SessionOption) (*Session, error) {
	opts := sessionOptions{timeout: storage.DefaultOpenTimeout}
	for _, option := range options {
		option(&opts)
	}

	if dbPath == storage.MemoryPath {
		store, err := storage.NewLocalStoreWithTimeout(storage.MemoryPath, opts.timeout)
		if err != nil {
			return nil, fmt.Errorf("open memory store: %w", err)
		}
		return openLocal(store, cfg, opts)
	}

	canon := dbPath
	if !opts.canonicalPath {
		var err error
		canon, err = ipc.CanonicalDBPath(dbPath)
		if err != nil {
			return nil, fmt.Errorf("canonicalize db path: %w", err)
		}
	}
	ep := ipc.NewEndpoint(canon)

	probeErr := ipc.Probe(ep.Address, ep.DatabaseID)
	if probeErr == nil {
		return openRemote(ep, cfg)
	}

	store, err := storage.NewLocalStoreWithTimeout(canon, opts.timeout)
	if err != nil {
		if errors.Is(err, storage.ErrDatabaseLocked) {
			probeErr = ipc.Probe(ep.Address, ep.DatabaseID)
			if probeErr == nil {
				return openRemote(ep, cfg)
			}
			return nil, fmt.Errorf("%w (ipc probe: %v)", err, probeErr)
		}
		return nil, fmt.Errorf("open store: %w", err)
	}
	return openLocal(store, cfg, opts)
}

func openLocal(store *storage.Store, cfg *config.Config, opts sessionOptions) (*Session, error) {
	manager := feed.NewManager(store, cfg)
	session := &Session{
		Store: store,
		Feeds: NewMutationCoordinator(&LocalOps{Store: store, Manager: manager}),
	}
	if opts.localSetup != nil {
		closer, err := opts.localSetup(store, manager)
		if err != nil {
			if closer != nil {
				_ = closer.Close()
			}
			_ = store.Close()
			return nil, fmt.Errorf("configure local session: %w", err)
		}
		if closer != nil {
			session.closers = append(session.closers, closer)
		}
	}
	return session, nil
}

// IsRemote reports whether this session is backed by the serve process.
func (s *Session) IsRemote() bool { return s != nil && s.client != nil }

func openRemote(ep ipc.Endpoint, cfg *config.Config) (*Session, error) {
	client, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		return nil, fmt.Errorf("connect to serve: %w", err)
	}

	// Keep long-running feed operations off the storage connection so reads
	// and reader-state changes remain responsive during a refresh/import.
	operations, err := ipc.NewClient(ep.Address, ep.DatabaseID)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect feed operations to serve: %w", err)
	}
	return &Session{
		Store:   storage.NewRPCStore(client),
		Feeds:   newRemoteOps(operations, cfg),
		client:  client,
		closers: []io.Closer{operations},
	}, nil
}
