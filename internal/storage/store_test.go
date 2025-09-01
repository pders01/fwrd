package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setupTestStore(t *testing.T) (*Store, func()) {
	tmpDir, err := os.MkdirTemp("", "store-test-*")
	if err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := NewStore(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatal(err)
	}

	cleanup := func() {
		store.Close()
		os.RemoveAll(tmpDir)
	}

	return store, cleanup
}

func TestStore_SaveAndGetFeed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	feed := &Feed{
		ID:           "test-feed-1",
		URL:          "http://example.com/feed.xml",
		Title:        "Test Feed",
		Description:  "A test feed",
		LastFetched:  time.Now(),
		ETag:         "\"abc123\"",
		LastModified: "Wed, 01 Jan 2025 00:00:00 GMT",
		UpdatedAt:    time.Now(),
	}

	err := store.SaveFeed(feed)
	if err != nil {
		t.Fatalf("failed to save feed: %v", err)
	}

	retrieved, err := store.GetFeed("test-feed-1")
	if err != nil {
		t.Fatalf("failed to get feed: %v", err)
	}

	if retrieved.ID != feed.ID {
		t.Errorf("expected ID %s, got %s", feed.ID, retrieved.ID)
	}
	if retrieved.URL != feed.URL {
		t.Errorf("expected URL %s, got %s", feed.URL, retrieved.URL)
	}
	if retrieved.Title != feed.Title {
		t.Errorf("expected Title %s, got %s", feed.Title, retrieved.Title)
	}
	if retrieved.ETag != feed.ETag {
		t.Errorf("expected ETag %s, got %s", feed.ETag, retrieved.ETag)
	}
}

func TestStore_GetFeed_NotFound(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	_, err := store.GetFeed("non-existent")
	if err == nil {
		t.Error("expected error for non-existent feed, got nil")
	}
}

func TestStore_GetAllFeeds(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	feeds := []*Feed{
		{ID: "feed1", URL: "http://example.com/feed1.xml", Title: "Feed 1"},
		{ID: "feed2", URL: "http://example.com/feed2.xml", Title: "Feed 2"},
		{ID: "feed3", URL: "http://example.com/feed3.xml", Title: "Feed 3"},
	}

	for _, feed := range feeds {
		if err := store.SaveFeed(feed); err != nil {
			t.Fatalf("failed to save feed: %v", err)
		}
	}

	allFeeds, err := store.GetAllFeeds()
	if err != nil {
		t.Fatalf("failed to get all feeds: %v", err)
	}

	if len(allFeeds) != len(feeds) {
		t.Errorf("expected %d feeds, got %d", len(feeds), len(allFeeds))
	}

	feedMap := make(map[string]*Feed)
	for _, f := range allFeeds {
		feedMap[f.ID] = f
	}

	for _, expectedFeed := range feeds {
		if f, ok := feedMap[expectedFeed.ID]; !ok {
			t.Errorf("feed %s not found", expectedFeed.ID)
		} else if f.Title != expectedFeed.Title {
			t.Errorf("feed %s: expected title %s, got %s", expectedFeed.ID, expectedFeed.Title, f.Title)
		}
	}
}

func TestStore_SaveAndGetArticles(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	articles := []*Article{
		{
			ID:          "article1",
			FeedID:      "feed1",
			Title:       "Article 1",
			Description: "Description 1",
			Content:     "Content 1",
			URL:         "http://example.com/article1",
			Published:   time.Now().Add(-2 * time.Hour),
			Read:        false,
			MediaURLs:   []string{"http://example.com/image1.jpg"},
		},
		{
			ID:          "article2",
			FeedID:      "feed1",
			Title:       "Article 2",
			Description: "Description 2",
			Content:     "Content 2",
			URL:         "http://example.com/article2",
			Published:   time.Now().Add(-1 * time.Hour),
			Read:        true,
		},
		{
			ID:          "article3",
			FeedID:      "feed2",
			Title:       "Article 3",
			Description: "Description 3",
			URL:         "http://example.com/article3",
			Published:   time.Now(),
			Read:        false,
		},
	}

	err := store.SaveArticles(articles)
	if err != nil {
		t.Fatalf("failed to save articles: %v", err)
	}

	feed1Articles, err := store.GetArticles("feed1", 10)
	if err != nil {
		t.Fatalf("failed to get articles: %v", err)
	}

	if len(feed1Articles) != 2 {
		t.Errorf("expected 2 articles for feed1, got %d", len(feed1Articles))
	}

	allArticles, err := store.GetArticles("", 10)
	if err != nil {
		t.Fatalf("failed to get all articles: %v", err)
	}

	if len(allArticles) != 3 {
		t.Errorf("expected 3 total articles, got %d", len(allArticles))
	}
}

func TestStore_MarkArticleRead(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	article := &Article{
		ID:     "article-test",
		FeedID: "feed-test",
		Title:  "Test Article",
		Read:   false,
	}

	err := store.SaveArticles([]*Article{article})
	if err != nil {
		t.Fatalf("failed to save article: %v", err)
	}

	err = store.MarkArticleRead("article-test", true)
	if err != nil {
		t.Fatalf("failed to mark article as read: %v", err)
	}

	articles, err := store.GetArticles("feed-test", 1)
	if err != nil {
		t.Fatalf("failed to get articles: %v", err)
	}

	if len(articles) != 1 {
		t.Fatalf("expected 1 article, got %d", len(articles))
	}

	if !articles[0].Read {
		t.Error("article should be marked as read")
	}

	err = store.MarkArticleRead("article-test", false)
	if err != nil {
		t.Fatalf("failed to mark article as unread: %v", err)
	}

	articles, err = store.GetArticles("feed-test", 1)
	if err != nil {
		t.Fatalf("failed to get articles: %v", err)
	}

	if articles[0].Read {
		t.Error("article should be marked as unread")
	}
}

func TestStore_DeleteFeed(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	feed := &Feed{
		ID:    "feed-to-delete",
		URL:   "http://example.com/feed.xml",
		Title: "Feed to Delete",
	}

	err := store.SaveFeed(feed)
	if err != nil {
		t.Fatalf("failed to save feed: %v", err)
	}

	articles := []*Article{
		{ID: "article1", FeedID: "feed-to-delete", Title: "Article 1"},
		{ID: "article2", FeedID: "feed-to-delete", Title: "Article 2"},
		{ID: "article3", FeedID: "other-feed", Title: "Article 3"},
	}

	err = store.SaveArticles(articles)
	if err != nil {
		t.Fatalf("failed to save articles: %v", err)
	}

	err = store.DeleteFeed("feed-to-delete")
	if err != nil {
		t.Fatalf("failed to delete feed: %v", err)
	}

	_, err = store.GetFeed("feed-to-delete")
	if err == nil {
		t.Error("expected error when getting deleted feed")
	}

	remainingArticles, err := store.GetArticles("", 10)
	if err != nil {
		t.Fatalf("failed to get articles: %v", err)
	}

	if len(remainingArticles) != 1 {
		t.Errorf("expected 1 remaining article, got %d", len(remainingArticles))
	}

	if remainingArticles[0].FeedID != "other-feed" {
		t.Error("wrong article remained after feed deletion")
	}
}

func TestStore_GetArticles_Limit(t *testing.T) {
	store, cleanup := setupTestStore(t)
	defer cleanup()

	articles := make([]*Article, 20)
	for i := 0; i < 20; i++ {
		articles[i] = &Article{
			ID:        fmt.Sprintf("article%d", i),
			FeedID:    "feed1",
			Title:     fmt.Sprintf("Article %d", i),
			Published: time.Now().Add(time.Duration(-i) * time.Hour),
		}
	}

	err := store.SaveArticles(articles)
	if err != nil {
		t.Fatalf("failed to save articles: %v", err)
	}

	limitedArticles, err := store.GetArticles("feed1", 5)
	if err != nil {
		t.Fatalf("failed to get articles with limit: %v", err)
	}

	if len(limitedArticles) != 5 {
		t.Errorf("expected 5 articles with limit, got %d", len(limitedArticles))
	}
}