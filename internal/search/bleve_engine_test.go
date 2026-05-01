package search

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestBleveEngineIndexesFeedLargerThanChunkSize seeds a feed with more
// articles than maxArticlesPerFeed to verify cursor-based chunked indexing
// terminates and indexes the full set. The previous offset-based loop
// re-fetched the same first chunk forever for feeds at or above the chunk
// size.
func TestBleveEngineIndexesFeedLargerThanChunkSize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "large.db")
	store, err := storage.NewStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	feed := &storage.Feed{ID: "big", Title: "Big Feed", URL: "https://example.com/big"}
	require.NoError(t, store.SaveFeed(feed))

	const total = maxArticlesPerFeed + 500 // exceeds one chunk
	base := time.Now().Add(-time.Duration(total) * time.Minute)
	arts := make([]*storage.Article, total)
	for i := range total {
		arts[i] = &storage.Article{
			ID:        fmt.Sprintf("a%05d", i),
			FeedID:    feed.ID,
			Title:     fmt.Sprintf("article %05d unicornsentinel", i),
			URL:       fmt.Sprintf("https://example.com/big/%d", i),
			Published: base.Add(time.Duration(i) * time.Minute),
		}
	}
	require.NoError(t, store.SaveArticles(arts))

	idxPath := filepath.Join(dir, "index.bleve")

	// Guard against regression to infinite loop: bound the indexing time.
	done := make(chan struct{})
	var eng Searcher
	go func() {
		defer close(done)
		eng, err = NewBleveEngine(store, idxPath)
	}()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("NewBleveEngine did not return within 30s — chunked indexing likely looping")
	}
	require.NoError(t, err)
	require.NotNil(t, eng)

	// Sentinel hit-count check: every article carries the unique token
	// "unicornsentinel". A correctly indexed feed returns the requested
	// limit; the broken loop only ever indexed the first chunk.
	res, err := eng.Search("unicornsentinel", total)
	require.NoError(t, err)
	require.Equal(t, total, len(res), "expected all articles indexed, got %d", len(res))
}

// TestBleveEngineOnFeedDeleted_RemovesAllArticles asserts that
// OnFeedDeleted clears every indexed article belonging to the feed,
// including counts above one bleve search page (pageSize=1000).
// The earlier `from += size` pagination skipped docs that shifted
// down after the first batch was deleted.
func TestBleveEngineOnFeedDeleted_RemovesAllArticles(t *testing.T) {
	dir := t.TempDir()
	store, err := storage.NewStore(filepath.Join(dir, "del.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	feed := &storage.Feed{ID: "victim", Title: "Victim", URL: "https://example.com/v"}
	require.NoError(t, store.SaveFeed(feed))

	const total = 1100 // 1 full page + 100 — broken pagination would have leaked the tail
	arts := make([]*storage.Article, total)
	base := time.Now().Add(-time.Duration(total) * time.Minute)
	for i := range total {
		arts[i] = &storage.Article{
			ID:        fmt.Sprintf("v%05d", i),
			FeedID:    feed.ID,
			Title:     fmt.Sprintf("victimsentinel %05d", i),
			Published: base.Add(time.Duration(i) * time.Minute),
		}
	}
	require.NoError(t, store.SaveArticles(arts))

	eng, err := NewBleveEngine(store, filepath.Join(dir, "idx.bleve"))
	require.NoError(t, err)

	pre, err := eng.Search("victimsentinel", total)
	require.NoError(t, err)
	require.Equal(t, total, len(pre), "indexer did not seed full set")

	dl, ok := eng.(interface{ OnFeedDeleted(string) })
	require.True(t, ok, "engine must implement OnFeedDeleted")
	dl.OnFeedDeleted(feed.ID)

	post, err := eng.Search("victimsentinel", total)
	require.NoError(t, err)
	require.Equal(t, 0, len(post), "expected zero hits after deletion, got %d", len(post))
}
