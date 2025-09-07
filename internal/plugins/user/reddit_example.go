package user

import (
	"context"
	"net/http"
	"strings"

	"github.com/pders01/fwrd/internal/plugins"
)

// RedditPlugin handles Reddit subreddit URLs by converting them to RSS feeds
type RedditPlugin struct{}

// NewRedditPlugin creates a new Reddit plugin
func NewRedditPlugin() *RedditPlugin {
	return &RedditPlugin{}
}

// Name returns the plugin name
func (p *RedditPlugin) Name() string {
	return "reddit"
}

// CanHandle returns true if this plugin can handle Reddit subreddit URLs
func (p *RedditPlugin) CanHandle(url string) bool {
	return strings.Contains(url, "://www.reddit.com/r/") ||
		strings.Contains(url, "://reddit.com/r/")
}

// Priority returns the plugin priority
func (p *RedditPlugin) Priority() int {
	return 50
}

// EnhanceFeed processes Reddit URLs and returns enhanced feed information
func (p *RedditPlugin) EnhanceFeed(_ context.Context, rawURL string, _ *http.Client) (*plugins.FeedInfo, error) {
	// Reddit supports RSS by adding .rss to subreddit URLs
	feedURL := strings.TrimSuffix(rawURL, "/") + ".rss"

	// Extract subreddit name for title
	parts := strings.Split(rawURL, "/r/")
	subreddit := "unknown"
	if len(parts) > 1 {
		subreddit = strings.Split(parts[1], "/")[0]
	}

	return &plugins.FeedInfo{
		OriginalURL: rawURL,
		FeedURL:     feedURL,
		Title:       "Reddit - r/" + subreddit,
		Description: "Posts from r/" + subreddit + " subreddit",
		Metadata: map[string]string{
			"plugin":    "reddit",
			"subreddit": subreddit,
		},
	}, nil
}
