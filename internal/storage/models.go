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

type FetchMetadata struct {
	FeedID       string    `json:"feed_id"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"last_modified"`
	LastFetched  time.Time `json:"last_fetched"`
	NextFetch    time.Time `json:"next_fetch"`
	RetryAfter   int       `json:"retry_after"`
}