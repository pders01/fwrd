//go:build bleve

package search

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pders01/fwrd/internal/storage"
)

func TestBleveEngineIndexesAndSearches(t *testing.T) {
	// Create temp DB
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	store, err := storage.NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	// Seed a feed and two articles
	feed := &storage.Feed{ID: "f1", Title: "Test Feed", URL: "https://example.com/feed"}
	require.NoError(t, store.SaveFeed(feed))

	arts := []*storage.Article{
		{ID: "a1", FeedID: feed.ID, Title: "Hello World", Description: "greeting article", URL: "https://example.com/1"},
		{ID: "a2", FeedID: feed.ID, Title: "Golang Tips", Description: "bleve and search", URL: "https://example.com/2", Content: "Using bleve for full text search"},
	}
	require.NoError(t, store.SaveArticles(arts))

	// Create bleve index
	idxPath := filepath.Join(dir, "index.bleve")
	eng, err := NewBleveEngine(store, idxPath)
	require.NoError(t, err)

	// Perform searches that should hit title/description/content
	res, err := eng.Search("Golang", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(res), 1)

	res, err = eng.Search("bleve", 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(res), 1)

	// Ensure index directory created
	fi, err := os.Stat(idxPath)
	require.NoError(t, err)
	require.True(t, fi.IsDir())
}
