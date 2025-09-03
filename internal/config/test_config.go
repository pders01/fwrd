package config

import "time"

// TestConfig returns a config suitable for testing
func TestConfig() *Config {
	return &Config{
		Database: DatabaseConfig{
			Path:    ":memory:", // Use in-memory database for tests
			Timeout: 1 * time.Second,
		},
		Feed: FeedConfig{
			HTTPTimeout:       5 * time.Second,
			RefreshInterval:   1 * time.Minute,
			DefaultRetryAfter: 5 * time.Minute,
			UserAgent:         "fwrd-test/1.0",
		},
		UI:    defaultConfig().UI,
		Media: defaultConfig().Media,
		Keys:  defaultConfig().Keys,
	}
}
