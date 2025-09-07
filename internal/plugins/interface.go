package plugins

import (
	"context"
	"net/http"
	"time"
)

// FeedInfo represents enhanced feed information that a plugin can provide
type FeedInfo struct {
	// Original URL that was requested
	OriginalURL string
	// Enhanced URL (e.g., with proper RSS endpoint)
	FeedURL string
	// Enhanced title (e.g., "YouTube - @creator" instead of "www.youtube.com")
	Title string
	// Description of the feed
	Description string
	// Additional metadata that plugins can provide
	Metadata map[string]string
}

// Plugin defines the interface that host-specific plugins must implement
type Plugin interface {
	// Name returns the plugin name for identification
	Name() string

	// CanHandle returns true if this plugin can handle the given URL
	CanHandle(url string) bool

	// EnhanceFeed processes a URL and returns enhanced feed information
	// This may involve HTTP requests to fetch metadata, resolve redirects, etc.
	EnhanceFeed(ctx context.Context, url string, client *http.Client) (*FeedInfo, error)

	// Priority returns the priority of this plugin (higher = higher priority)
	// Useful when multiple plugins can handle the same URL
	Priority() int
}

// Registry manages all registered plugins
type Registry struct {
	plugins []Plugin
	client  *http.Client
}

// NewRegistry creates a new plugin registry
func NewRegistry(timeout time.Duration) *Registry {
	return &Registry{
		plugins: make([]Plugin, 0),
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

// Register adds a plugin to the registry
func (r *Registry) Register(plugin Plugin) {
	r.plugins = append(r.plugins, plugin)
}

// FindPlugin returns the best plugin for handling a given URL
// Returns the plugin with highest priority that can handle the URL
func (r *Registry) FindPlugin(url string) Plugin {
	var bestPlugin Plugin
	highestPriority := -1

	for _, plugin := range r.plugins {
		if plugin.CanHandle(url) && plugin.Priority() > highestPriority {
			bestPlugin = plugin
			highestPriority = plugin.Priority()
		}
	}

	return bestPlugin
}

// EnhanceFeed attempts to enhance feed information using available plugins
func (r *Registry) EnhanceFeed(ctx context.Context, url string) (*FeedInfo, error) {
	plugin := r.FindPlugin(url)
	if plugin == nil {
		// No plugin available, return basic info
		return &FeedInfo{
			OriginalURL: url,
			FeedURL:     url,
			Title:       "",
			Description: "",
			Metadata:    make(map[string]string),
		}, nil
	}

	return plugin.EnhanceFeed(ctx, url, r.client)
}

// ListPlugins returns all registered plugins
func (r *Registry) ListPlugins() []Plugin {
	return append([]Plugin(nil), r.plugins...)
}
