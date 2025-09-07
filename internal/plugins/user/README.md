# User Plugins Directory

This directory is for custom plugins that users can add without committing them to the main repository.

## Adding Custom Plugins

1. Create your plugin file in this directory (e.g., `myplugin.go`)
2. Implement the `Plugin` interface from `internal/plugins`
3. Register your plugin in the feed manager

Example plugin structure:
```go
package user

import (
    "context"
    "net/http"
    "github.com/pders01/fwrd/internal/plugins"
)

type MyPlugin struct{}

func NewMyPlugin() *MyPlugin {
    return &MyPlugin{}
}

func (p *MyPlugin) Name() string {
    return "myplugin"
}

func (p *MyPlugin) CanHandle(url string) bool {
    // Return true if this plugin should handle the URL
    return false
}

func (p *MyPlugin) Priority() int {
    return 50 // Plugin priority (higher = more priority)
}

func (p *MyPlugin) EnhanceFeed(ctx context.Context, url string, client *http.Client) (*plugins.FeedInfo, error) {
    // Enhance the feed URL and metadata
    return &plugins.FeedInfo{
        OriginalURL: url,
        FeedURL:     url, // Could be enhanced
        Title:       "", // Could be enhanced
        Description: "",
        Metadata:    make(map[string]string),
    }, nil
}
```

Then register it by modifying the feed manager initialization.

## Simple Example

Here's a simple Reddit plugin that converts subreddit URLs to RSS feeds:

```go
package user

import (
    "context"
    "net/http"
    "strings"
    "github.com/pders01/fwrd/internal/plugins"
)

type RedditPlugin struct{}

func NewRedditPlugin() *RedditPlugin {
    return &RedditPlugin{}
}

func (p *RedditPlugin) Name() string {
    return "reddit"
}

func (p *RedditPlugin) CanHandle(url string) bool {
    return strings.Contains(url, "://www.reddit.com/r/") ||
           strings.Contains(url, "://reddit.com/r/")
}

func (p *RedditPlugin) Priority() int {
    return 50
}

func (p *RedditPlugin) EnhanceFeed(ctx context.Context, url string, client *http.Client) (*plugins.FeedInfo, error) {
    // Reddit supports RSS by adding .rss to subreddit URLs
    feedURL := strings.TrimSuffix(url, "/") + ".rss"
    
    // Extract subreddit name for title
    parts := strings.Split(url, "/r/")
    subreddit := "unknown"
    if len(parts) > 1 {
        subreddit = strings.Split(parts[1], "/")[0]
    }
    
    return &plugins.FeedInfo{
        OriginalURL: url,
        FeedURL:     feedURL,
        Title:       "Reddit - r/" + subreddit,
        Description: "Posts from r/" + subreddit + " subreddit",
        Metadata:    map[string]string{"plugin": "reddit", "subreddit": subreddit},
    }, nil
}
```

**Note:** This directory is ignored by git, so your custom plugins won't be committed to the repository.