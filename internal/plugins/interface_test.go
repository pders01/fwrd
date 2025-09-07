package plugins

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockPlugin is a test plugin for testing the registry
type mockPlugin struct {
	name      string
	priority  int
	canHandle func(string) bool
	enhance   func(context.Context, string, *http.Client) (*FeedInfo, error)
}

func (p *mockPlugin) Name() string {
	return p.name
}

func (p *mockPlugin) CanHandle(url string) bool {
	if p.canHandle != nil {
		return p.canHandle(url)
	}
	return false
}

func (p *mockPlugin) EnhanceFeed(ctx context.Context, url string, client *http.Client) (*FeedInfo, error) {
	if p.enhance != nil {
		return p.enhance(ctx, url, client)
	}
	return &FeedInfo{
		OriginalURL: url,
		FeedURL:     url,
		Title:       "Mock Feed",
		Description: "Mock Description",
		Metadata:    make(map[string]string),
	}, nil
}

func (p *mockPlugin) Priority() int {
	return p.priority
}

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry(5 * time.Second)

	assert.NotNil(t, registry)
	assert.Equal(t, 0, len(registry.plugins))
	assert.NotNil(t, registry.client)
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry(5 * time.Second)
	plugin := &mockPlugin{name: "test", priority: 50}

	registry.Register(plugin)

	assert.Equal(t, 1, len(registry.plugins))
	assert.Equal(t, plugin, registry.plugins[0])
}

func TestRegistry_FindPlugin(t *testing.T) {
	registry := NewRegistry(5 * time.Second)

	// Register plugins with different priorities
	plugin1 := &mockPlugin{
		name:     "low-priority",
		priority: 10,
		canHandle: func(url string) bool {
			return url == "http://example.com"
		},
	}

	plugin2 := &mockPlugin{
		name:     "high-priority",
		priority: 100,
		canHandle: func(url string) bool {
			return url == "http://example.com"
		},
	}

	plugin3 := &mockPlugin{
		name:     "different-url",
		priority: 200,
		canHandle: func(url string) bool {
			return url == "http://other.com"
		},
	}

	registry.Register(plugin1)
	registry.Register(plugin2)
	registry.Register(plugin3)

	t.Run("finds highest priority plugin", func(t *testing.T) {
		result := registry.FindPlugin("http://example.com")
		assert.Equal(t, plugin2, result)
	})

	t.Run("finds specific plugin", func(t *testing.T) {
		result := registry.FindPlugin("http://other.com")
		assert.Equal(t, plugin3, result)
	})

	t.Run("returns nil for no matching plugin", func(t *testing.T) {
		result := registry.FindPlugin("http://nomatch.com")
		assert.Nil(t, result)
	})
}

func TestRegistry_EnhanceFeed(t *testing.T) {
	registry := NewRegistry(5 * time.Second)

	t.Run("with matching plugin", func(t *testing.T) {
		plugin := &mockPlugin{
			name:     "test",
			priority: 50,
			canHandle: func(url string) bool {
				return url == "http://test.com"
			},
			enhance: func(_ context.Context, url string, _ *http.Client) (*FeedInfo, error) {
				return &FeedInfo{
					OriginalURL: url,
					FeedURL:     "http://test.com/feed.xml",
					Title:       "Enhanced Title",
					Description: "Enhanced Description",
					Metadata:    map[string]string{"plugin": "test"},
				}, nil
			},
		}

		registry.Register(plugin)

		ctx := context.Background()
		result, err := registry.EnhanceFeed(ctx, "http://test.com")

		require.NoError(t, err)
		assert.Equal(t, "http://test.com", result.OriginalURL)
		assert.Equal(t, "http://test.com/feed.xml", result.FeedURL)
		assert.Equal(t, "Enhanced Title", result.Title)
		assert.Equal(t, "Enhanced Description", result.Description)
		assert.Equal(t, "test", result.Metadata["plugin"])
	})

	t.Run("without matching plugin", func(t *testing.T) {
		registry := NewRegistry(5 * time.Second)

		ctx := context.Background()
		result, err := registry.EnhanceFeed(ctx, "http://nomatch.com")

		require.NoError(t, err)
		assert.Equal(t, "http://nomatch.com", result.OriginalURL)
		assert.Equal(t, "http://nomatch.com", result.FeedURL)
		assert.Equal(t, "", result.Title)
		assert.Equal(t, "", result.Description)
		assert.NotNil(t, result.Metadata)
	})
}

func TestRegistry_ListPlugins(t *testing.T) {
	registry := NewRegistry(5 * time.Second)

	plugin1 := &mockPlugin{name: "plugin1", priority: 10}
	plugin2 := &mockPlugin{name: "plugin2", priority: 20}

	registry.Register(plugin1)
	registry.Register(plugin2)

	plugins := registry.ListPlugins()

	assert.Equal(t, 2, len(plugins))
	assert.Contains(t, plugins, plugin1)
	assert.Contains(t, plugins, plugin2)

	// Verify it returns a copy (modifying returned slice doesn't affect registry)
	plugins[0] = nil
	assert.Equal(t, 2, len(registry.plugins))
	assert.NotNil(t, registry.plugins[0])
}

func TestFeedInfoBasics(t *testing.T) {
	info := &FeedInfo{
		OriginalURL: "http://example.com",
		FeedURL:     "http://example.com/feed.xml",
		Title:       "Test Feed",
		Description: "Test Description",
		Metadata:    map[string]string{"key": "value"},
	}

	assert.Equal(t, "http://example.com", info.OriginalURL)
	assert.Equal(t, "http://example.com/feed.xml", info.FeedURL)
	assert.Equal(t, "Test Feed", info.Title)
	assert.Equal(t, "Test Description", info.Description)
	assert.Equal(t, "value", info.Metadata["key"])
}
