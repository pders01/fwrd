package plugins

import (
	"context"
	"errors"
	"net/http"
	urlpkg "net/url"
	"sync"
	"time"

	"github.com/pders01/fwrd/internal/audit"
	"github.com/pders01/fwrd/internal/validation"
)

// FeedInfo represents enhanced feed information that a plugin can provide.
type FeedInfo struct {
	OriginalURL string
	FeedURL     string
	Title       string
	Description string
	Metadata    map[string]string
}

// Plugin defines the interface host-specific plugins implement. Native Go
// plugins are trusted; script plugins must also implement ContextCanHandler.
type Plugin interface {
	Name() string
	CanHandle(url string) bool
	EnhanceFeed(ctx context.Context, url string, client *http.Client) (*FeedInfo, error)
	Priority() int
}

// ContextCanHandler supports cancellation while waiting for and running routing.
type ContextCanHandler interface {
	CanHandleContext(context.Context, string) bool
}

type entry struct {
	plugin Plugin
	name   string
	path   string
}

func pluginEntry(p Plugin) entry {
	e := entry{plugin: p, name: p.Name()}
	if source, ok := p.(interface{ Path() string }); ok {
		e.path = source.Path()
	}
	return e
}

// Registry is safe for concurrent use. No plugin code runs under its lock;
// metadata used by mutations is captured before acquiring the lock.
type Registry struct {
	mu      sync.RWMutex
	plugins []entry
	client  *http.Client
	timeout time.Duration
}

// NewRegistry creates a registry with a strict outbound HTTP client.
func NewRegistry(timeout time.Duration) *Registry {
	return &Registry{client: validation.NewHTTPClient(timeout), timeout: timeout}
}

// SetHTTPClient shares a host's policy-secured client with native plugins.
// A nil client is ignored, preserving secure defaults.
func (r *Registry) SetHTTPClient(client *http.Client) {
	if client == nil {
		return
	}
	r.mu.Lock()
	r.client = client
	r.mu.Unlock()
}

// Register adds a plugin to the registry.
func (r *Registry) Register(plugin Plugin) {
	e := pluginEntry(plugin)
	r.mu.Lock()
	r.plugins = append(r.plugins, e)
	r.mu.Unlock()
}

// Replace swaps a plugin by name and returns the displaced instance.
func (r *Registry) Replace(p Plugin) Plugin {
	e := pluginEntry(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, existing := range r.plugins {
		if existing.name == e.name {
			r.plugins[i] = e
			return existing.plugin
		}
	}
	r.plugins = append(r.plugins, e)
	return nil
}

// ReplacePath atomically replaces all entries owned by path, including when a
// script changes its declared name. A name collision at another path is also
// displaced. The caller must close every returned plugin, outside the lock.
func (r *Registry) ReplacePath(path string, p Plugin) []Plugin {
	e := pluginEntry(p)
	e.path = path
	r.mu.Lock()
	defer r.mu.Unlock()
	var displaced []Plugin
	kept := make([]entry, 0, len(r.plugins)+1)
	for _, existing := range r.plugins {
		if existing.path == path || existing.name == e.name {
			displaced = append(displaced, existing.plugin)
		} else {
			kept = append(kept, existing)
		}
	}
	kept = append(kept, e)
	r.plugins = kept
	return displaced
}

// Unregister removes the plugin with the given name.
func (r *Registry) Unregister(name string) Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i, p := range r.plugins {
		if p.name == name {
			r.plugins = append(r.plugins[:i], r.plugins[i+1:]...)
			return p.plugin
		}
	}
	return nil
}

// UnregisterPath atomically removes only plugins owned by path.
func (r *Registry) UnregisterPath(path string) []Plugin {
	r.mu.Lock()
	defer r.mu.Unlock()
	var removed []Plugin
	kept := make([]entry, 0, len(r.plugins))
	for _, p := range r.plugins {
		if p.path == path {
			removed = append(removed, p.plugin)
		} else {
			kept = append(kept, p)
		}
	}
	r.plugins = kept
	return removed
}

// FindPlugin returns the highest-priority plugin that handles url.
func (r *Registry) FindPlugin(url string) Plugin {
	p, _ := r.FindPluginContext(context.Background(), url)
	return p
}

// FindPluginContext propagates cancellation to script routing and admission.
func (r *Registry) FindPluginContext(ctx context.Context, url string) (Plugin, error) {
	if r.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.timeout)
		defer cancel()
	}
	var best Plugin
	highest := -1
	for _, p := range r.ListPlugins() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var handles bool
		if cp, ok := p.(ContextCanHandler); ok {
			handles = cp.CanHandleContext(ctx, url)
		} else {
			handles = p.CanHandle(url)
		}
		if handles {
			if priority := p.Priority(); priority > highest {
				best, highest = p, priority
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return best, nil
}

// EnhanceFeed uses the best plugin, or returns the original URL unchanged.
// Callers must validate FeedURL before persisting or using it as a feed URL.
func (r *Registry) EnhanceFeed(ctx context.Context, url string) (*FeedInfo, error) {
	plugin, err := r.FindPluginContext(ctx, url)
	if err != nil {
		return nil, err
	}
	if plugin == nil {
		return &FeedInfo{OriginalURL: url, FeedURL: url, Metadata: make(map[string]string)}, nil
	}
	r.mu.RLock()
	client := r.client
	r.mu.RUnlock()
	info, err := plugin.EnhanceFeed(ctx, url, client)
	var urlErr *urlpkg.Error
	if errors.As(err, &urlErr) {
		err = audit.SafeHTTPError(err)
	}
	return info, err
}

// ListPlugins returns a snapshot; plugin calls must occur after unlocking.
func (r *Registry) ListPlugins() []Plugin {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Plugin, 0, len(r.plugins))
	for _, p := range r.plugins {
		out = append(out, p.plugin)
	}
	return out
}
