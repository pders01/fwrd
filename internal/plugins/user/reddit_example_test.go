package user

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/plugins"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedditPlugin_Name(t *testing.T) {
	plugin := NewRedditPlugin()
	assert.Equal(t, "reddit", plugin.Name())
}

func TestRedditPlugin_Priority(t *testing.T) {
	plugin := NewRedditPlugin()
	assert.Equal(t, 50, plugin.Priority())
}

func TestRedditPlugin_CanHandle(t *testing.T) {
	plugin := NewRedditPlugin()

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "reddit.com subreddit URL",
			url:      "https://www.reddit.com/r/golang",
			expected: true,
		},
		{
			name:     "reddit.com without www",
			url:      "https://reddit.com/r/programming",
			expected: true,
		},
		{
			name:     "reddit.com with trailing slash",
			url:      "https://www.reddit.com/r/golang/",
			expected: true,
		},
		{
			name:     "non-Reddit URL",
			url:      "https://example.com/feed",
			expected: false,
		},
		{
			name:     "reddit.com but not subreddit",
			url:      "https://www.reddit.com/user/someuser",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := plugin.CanHandle(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestRedditPlugin_EnhanceFeed(t *testing.T) {
	plugin := NewRedditPlugin()
	client := &http.Client{Timeout: 5 * time.Second}
	ctx := context.Background()

	tests := []struct {
		name              string
		url               string
		expectedFeedURL   string
		expectedTitle     string
		expectedSubreddit string
	}{
		{
			name:              "simple subreddit URL",
			url:               "https://www.reddit.com/r/golang",
			expectedFeedURL:   "https://www.reddit.com/r/golang.rss",
			expectedTitle:     "Reddit - r/golang",
			expectedSubreddit: "golang",
		},
		{
			name:              "subreddit URL with trailing slash",
			url:               "https://www.reddit.com/r/programming/",
			expectedFeedURL:   "https://www.reddit.com/r/programming.rss",
			expectedTitle:     "Reddit - r/programming",
			expectedSubreddit: "programming",
		},
		{
			name:              "reddit.com without www",
			url:               "https://reddit.com/r/rust",
			expectedFeedURL:   "https://reddit.com/r/rust.rss",
			expectedTitle:     "Reddit - r/rust",
			expectedSubreddit: "rust",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := plugin.EnhanceFeed(ctx, tt.url, client)

			require.NoError(t, err)
			assert.Equal(t, tt.url, result.OriginalURL)
			assert.Equal(t, tt.expectedFeedURL, result.FeedURL)
			assert.Equal(t, tt.expectedTitle, result.Title)
			assert.Contains(t, result.Description, tt.expectedSubreddit)
			assert.Equal(t, "reddit", result.Metadata["plugin"])
			assert.Equal(t, tt.expectedSubreddit, result.Metadata["subreddit"])
		})
	}
}

func TestRedditPlugin_Integration(t *testing.T) {
	// Integration test that demonstrates the full plugin workflow
	plugin := NewRedditPlugin()
	registry := plugins.NewRegistry(5 * time.Second)
	registry.Register(plugin)

	ctx := context.Background()

	t.Run("Reddit subreddit URL enhancement", func(t *testing.T) {
		url := "https://www.reddit.com/r/golang"

		result, err := registry.EnhanceFeed(ctx, url)

		require.NoError(t, err)
		assert.Equal(t, url, result.OriginalURL)
		assert.Equal(t, "https://www.reddit.com/r/golang.rss", result.FeedURL)
		assert.Equal(t, "Reddit - r/golang", result.Title)
		assert.Contains(t, result.Metadata, "plugin")
		assert.Equal(t, "reddit", result.Metadata["plugin"])
		assert.Equal(t, "golang", result.Metadata["subreddit"])
	})

	t.Run("non-Reddit URL fallback", func(t *testing.T) {
		url := "https://example.com/feed"

		result, err := registry.EnhanceFeed(ctx, url)

		require.NoError(t, err)
		assert.Equal(t, url, result.OriginalURL)
		assert.Equal(t, url, result.FeedURL)       // No enhancement
		assert.Equal(t, "", result.Title)          // No enhancement
		assert.Empty(t, result.Metadata["plugin"]) // No plugin used
	})
}
