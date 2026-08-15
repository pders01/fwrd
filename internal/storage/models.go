package storage

import (
	"time"
)

type Feed struct {
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	Title        string    `json:"title"`
	Description  string    `json:"description"`
	LastFetched  time.Time `json:"last_fetched"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"last_modified"`
	UpdatedAt    time.Time `json:"updated_at"`
	// DeskID is the stable, user-assigned editorial desk. An empty value is
	// retained for backward compatibility and is presented as "unfiled".
	DeskID string `json:"desk_id,omitempty"`
	// LastError holds the message from the most recent failed refresh, or
	// "" when the last attempt succeeded. LastErrorAt timestamps that
	// failure. LastFetched still tracks the last *successful* fetch, so the
	// two together distinguish "stale because failing" from "just stale".
	LastError   string    `json:"last_error,omitempty"`
	LastErrorAt time.Time `json:"last_error_at,omitzero"`
}

type Article struct {
	ID          string    `json:"id"`
	FeedID      string    `json:"feed_id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Content     string    `json:"content"`
	URL         string    `json:"url"`
	Published   time.Time `json:"published"`
	Updated     time.Time `json:"updated"`
	Read        bool      `json:"read"`
	Starred     bool      `json:"starred"`
	MediaURLs   []string  `json:"media_urls"`
}

// FeedStat holds the cheap per-feed article counts surfaced on the
// feed-management page.
type FeedStat struct {
	Unread int
	Total  int
}
