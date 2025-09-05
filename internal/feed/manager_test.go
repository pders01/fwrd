package feed

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func TestNewManager(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store, cfg)

	assert.NotNil(t, manager)
	assert.NotNil(t, manager.fetcher)
	assert.Equal(t, store, manager.store)
}

func TestRefreshAllFeeds(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store, cfg)

	// This will try to refresh all feeds (which will be none)
	err = manager.RefreshAllFeeds()
	assert.NoError(t, err)
}

func TestAddFeed(t *testing.T) {
	cfg := config.TestConfig()
	store, err := storage.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}
	defer store.Close()

	manager := NewManager(store, cfg)

	// Test adding a feed (will fail with actual network request but tests the structure)
	t.Run("Add feed with invalid URL", func(t *testing.T) {
		feed, err := manager.AddFeed("not-a-url")
		assert.Error(t, err)
		assert.Nil(t, feed)
	})

	t.Run("Add feed with valid URL format", func(t *testing.T) {
		// This will try to actually fetch, so it will fail, but tests the URL validation
		feed, err := manager.AddFeed("http://example.com/feed.xml")
		// We expect an error because it will try to fetch from example.com
		assert.Error(t, err)
		assert.Nil(t, feed)
	})
}
