package search

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

func testBleve(t *testing.T) (*storage.Store, *bleveEngine, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := storage.NewStore(filepath.Join(dir, "db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	path := filepath.Join(dir, "index.bleve")
	engine, err := newBleveEngine(store, path, true)
	require.NoError(t, err)
	b := engine.(*bleveEngine)
	t.Cleanup(func() { require.NoError(t, b.Close()) })
	return store, b, path
}

func TestBleveHydratesAuthoritativeArticle(t *testing.T) {
	store, b, _ := testBleve(t)
	feed := &storage.Feed{ID: "feed", Title: "Feed", URL: "https://example.test/feed"}
	require.NoError(t, store.SaveFeed(feed))
	article := &storage.Article{ID: "article", FeedID: feed.ID, Title: "hydrationsentinel", Content: "full reader body", MediaURLs: []string{"https://example.test/video.mp4"}, Published: time.Now().UTC().Truncate(time.Second)}
	require.NoError(t, store.SaveArticles([]*storage.Article{article}))
	b.OnDataUpdated(feed, []*storage.Article{article})
	// User state changes do not trigger reindexing.
	require.NoError(t, store.MarkArticleRead(article.ID, true))
	require.NoError(t, store.MarkArticleStarred(article.ID, true))
	results, err := b.Search("hydrationsentinel", 10)
	require.NoError(t, err)
	require.Len(t, results, 1)
	got := results[0].Article
	require.Equal(t, article.Content, got.Content)
	require.Equal(t, article.MediaURLs, got.MediaURLs)
	require.Equal(t, article.Published, got.Published)
	require.True(t, got.Read)
	require.True(t, got.Starred)
	require.NoError(t, store.DeleteFeed(feed.ID))
	results, err = b.Search("hydrationsentinel", 10)
	require.NoError(t, err)
	require.Empty(t, results, "deleted articles must not be reconstructed from stale stored index fields")
}

func TestBleveBatchOwnsEntireScope(t *testing.T) {
	_, b, _ := testBleve(t)
	b.BeginBatch()
	b.OnDataUpdated(&storage.Feed{ID: "first", Title: "first"}, nil)
	attempting, acquired, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		close(attempting)
		b.BeginBatch()
		close(acquired)
		b.OnDataUpdated(&storage.Feed{ID: "second", Title: "second"}, nil)
		b.CommitBatch()
	}()
	<-attempting
	select {
	case <-acquired:
		t.Fatal("overlapping BeginBatch replaced another owner's pending batch")
	case <-time.After(30 * time.Millisecond):
	}
	// Notifications in one batch may themselves overlap.
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b.OnDataUpdated(&storage.Feed{ID: fmt.Sprint(i), Title: "concurrent"}, nil)
		}()
	}
	wg.Wait()
	b.CommitBatch()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("second batch never acquired ownership")
	}
	count, err := b.DocCount()
	require.NoError(t, err)
	require.Equal(t, 22, count)
}

func TestBleveCloseDrainsBatchAndAllowsIndexReuse(t *testing.T) {
	store, b, path := testBleve(t)
	b.BeginBatch()
	b.OnDataUpdated(&storage.Feed{ID: "feed", Title: "close sentinel"}, nil)
	closing, closed := make(chan struct{}), make(chan struct{})
	go func() {
		close(closing)
		_ = b.Close()
		close(closed)
	}()
	<-closing
	select {
	case <-closed:
		t.Fatal("Close released an index while a batch still owned it")
	case <-time.After(30 * time.Millisecond):
	}
	b.CommitBatch()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not drain the batch")
	}
	require.NoError(t, b.Close())
	_, err := b.Search("sentinel", 10)
	require.Error(t, err)
	_, err = b.DocCount()
	require.Error(t, err)
	// Late callbacks and batches are safe no-ops, not use-after-close.
	b.BeginBatch()
	b.OnDataUpdated(&storage.Feed{ID: "late"}, nil)
	b.CommitBatch()
	b.OnFeedDeleted("feed")
	reopened, err := newBleveEngine(store, path, true)
	require.NoError(t, err)
	require.NoError(t, reopened.(*bleveEngine).Close())
}

func TestBleveConcurrentCallbacksSearchAndClose(t *testing.T) {
	_, b, _ := testBleve(t)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for j := range 8 {
				id := fmt.Sprintf("%d-%d", i, j)
				b.BeginBatch()
				b.OnDataUpdated(&storage.Feed{ID: id, Title: "sentinel"}, nil)
				b.CommitBatch()
				_, _ = b.Search("sentinel", 10)
				_, _ = b.DocCount()
				b.OnFeedDeleted(id)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		_ = b.Close()
	}()
	close(start)
	wg.Wait()
}
