package tui

import (
	"errors"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func paginationApp(t *testing.T) *App {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	app := newApp(store, config.TestConfig(), nil, nil, true)
	t.Cleanup(app.Close)
	app.view = ViewArticles
	app.currentFeed = &storage.Feed{ID: "feed"}
	return app
}

func TestArticlePaginationFailurePreservesCursorAndAllowsRetry(t *testing.T) {
	app := paginationApp(t)
	article := &storage.Article{ID: "first", FeedID: "feed"}
	app.articles = []*storage.Article{article}
	app.articleList.SetItems([]list.Item{articleItem{article: article}})
	app.articlesCursor, app.articlesHasMore = "first", true
	if app.maybeLoadMoreArticles() == nil {
		t.Fatal("expected initial page request")
	}
	failure := errors.New("broken pipe")
	_, cmd := app.Update(articlesLoadedMsg{feedID: "feed", seq: app.articlesSeq, appendPage: true, err: failure})
	if app.articlesLoadingMore || cmd != nil {
		t.Fatal("failed page remains in flight or immediately retried")
	}
	if app.articlesCursor != "first" || !app.articlesHasMore || len(app.articles) != 1 {
		t.Fatal("failure discarded loaded items or cursor")
	}
	if !errors.Is(app.err, failure) {
		t.Fatalf("displayed error = %v", app.err)
	}
	app.Update(struct{}{}) // background messages must not create a retry loop
	if app.articlesLoadingMore {
		t.Fatal("background message retried failed page")
	}
	retry := app.maybeLoadMoreArticles()
	if retry == nil || !app.articlesLoadingMore {
		t.Fatal("page cannot be retried")
	}
	// The empty next page completes successfully without duplicating items.
	app.Update(retry())
	if app.articlesLoadingMore || app.articlesHasMore || len(app.articles) != 1 || app.err != nil {
		t.Fatal("retry did not complete pagination cleanly or clear the recovered error")
	}
}

func TestArticleRecoveryDoesNotHideUnrelatedError(t *testing.T) {
	app := paginationApp(t)
	app.articlesErr = errors.New("previous page failure")
	unrelated := errors.New("failed to save starred state")
	app.err = unrelated
	app.Update(app.loadArticles("feed")())
	if !errors.Is(app.err, unrelated) {
		t.Fatal("page recovery hid an unrelated failure")
	}
}

func TestArticlePageErrorsCarryRequestIdentity(t *testing.T) {
	app := paginationApp(t)
	cmd := app.loadMoreArticles("feed", "cursor")
	if err := app.store.Close(); err != nil {
		t.Fatal(err)
	}
	msg, ok := cmd().(articlesLoadedMsg)
	if !ok || msg.err == nil || msg.feedID != "feed" || msg.seq != app.articlesSeq || !msg.appendPage {
		t.Fatalf("failed page response = %#v", msg)
	}
}

func TestArticleResponsesRejectStaleFeedAndGeneration(t *testing.T) {
	for _, sameFeed := range []bool{false, true} {
		for _, appendPage := range []bool{false, true} {
			name := "different-feed"
			if sameFeed {
				name = "same-feed"
			}
			if appendPage {
				name += "-append"
			}
			t.Run(name, func(t *testing.T) {
				app := paginationApp(t)
				oldFeed := app.currentFeed.ID
				oldCmd := app.loadArticlesPage(oldFeed, "", appendPage)
				old := oldCmd().(articlesLoadedMsg)
				old.articles = []*storage.Article{{ID: "stale", FeedID: oldFeed}}
				if !sameFeed {
					app.currentFeed = &storage.Feed{ID: "other"}
				}
				newCmd := app.loadArticles(app.currentFeed.ID)
				fresh := newCmd().(articlesLoadedMsg)
				fresh.articles = []*storage.Article{{ID: "current", FeedID: app.currentFeed.ID}}
				app.Update(fresh)
				app.Update(old)
				if len(app.articles) != 1 || app.articles[0].ID != "current" {
					t.Fatalf("stale response replaced/appended list: %#v", app.articles)
				}
				app.articlesLoadingMore = true
				old.err = errors.New("stale failure")
				app.Update(old)
				if app.err != nil || !app.articlesLoadingMore {
					t.Fatal("stale failure affected current request")
				}
			})
		}
	}
}

func TestNewArticleLoadClearsPreviousFeedImmediately(t *testing.T) {
	app := paginationApp(t)
	article := &storage.Article{ID: "old", FeedID: "feed"}
	app.articles = []*storage.Article{article}
	app.articleList.SetItems([]list.Item{articleItem{article: article}})
	app.articlesCursor, app.articlesHasMore, app.articlesLoadingMore = "old", true, true
	app.loadArticles("other")
	if len(app.articles) != 0 || len(app.articleList.Items()) != 0 || app.articlesCursor != "" || app.articlesHasMore || app.articlesLoadingMore {
		t.Fatal("previous feed's state remains visible during new load")
	}
}

func TestArticlePageCompletionWhileReaderOpenClearsInFlightState(t *testing.T) {
	app := paginationApp(t)
	app.articlesLoadingMore = true
	cmd := app.loadMoreArticles("feed", "")
	app.view = ViewReader
	app.Update(cmd())
	if app.articlesLoadingMore {
		t.Fatal("page completed outside article view but left pagination in flight")
	}
}
