package tui

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/list"
	"github.com/pders01/fwrd/internal/control"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestInitialFeedReadThenFatalRefreshStillShowsSubscriptions(t *testing.T) {
	app := commandTestApp(t)
	require.NoError(t, app.store.SaveFeed(&storage.Feed{ID: "saved", Title: "Saved subscription"}))
	initial := app.loadFeeds()
	failure := errors.New("refresh transport failed")
	app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
		return feed.RefreshSummary{}, failure
	}}
	refresh := app.refreshFeeds()
	app.Update(initial()) // invalidated by refresh, before its fatal completion
	require.Empty(t, app.feeds)
	_, reload := app.Update(refresh())
	app.Update(refreshMetadataMessage(t, reload))
	require.ErrorIs(t, app.err, failure)
	require.Len(t, app.feedList.Items(), 1)
	require.Equal(t, "Saved subscription", app.feeds[0].Title)
}

func TestReverseExecutedRefreshesReconcileFinalPersistedState(t *testing.T) {
	for _, uncertain := range []bool{false, true} {
		t.Run(fmt.Sprint(uncertain), func(t *testing.T) {
			app := commandTestApp(t)
			f := &storage.Feed{ID: "f", Title: "Before"}
			require.NoError(t, app.store.SaveFeed(f))
			app.Update(app.loadFeeds()())
			article := &storage.Article{ID: "article", FeedID: f.ID}
			app.view, app.currentFeed, app.currentArticle = ViewArticles, f, article
			app.articles = []*storage.Article{article}
			app.articleList.SetItems([]list.Item{articleItem{article: article}})
			app.articlesSeq, app.articlesCursor, app.articlesHasMore = 7, "cursor", true
			calls := 0
			failure := errors.New("reply lost after commit")
			ops := &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
				calls++
				fresh := *f
				fresh.Title = fmt.Sprintf("execution %d", calls)
				require.NoError(t, app.store.SaveFeed(&fresh))
				if calls == 2 && uncertain {
					return feed.RefreshSummary{}, failure
				}
				return feed.RefreshSummary{UpdatedFeeds: calls}, nil
			}}
			app.feedOps = control.NewMutationCoordinator(ops)
			a, b := app.refreshFeeds(), app.refreshFeeds()
			app.startSpinner(MsgRefreshing)
			// B executes first through the same coordinator. A is stale by
			// request generation but commits the final database state.
			_, reloadB := app.Update(b())
			readB := refreshMetadataMessage(t, reloadB)
			app.Update(readB)
			require.Equal(t, "execution 1", app.feeds[0].Title)
			status, kind := app.statusText, app.statusKind
			_, reloadA := app.Update(a())
			readA := refreshMetadataMessage(t, reloadA)
			require.Greater(t, readA.seq, readB.seq)
			app.Update(readA)
			app.Update(readB) // an older metadata read must still be rejected
			app.Update(errorMsg{err: errors.New("old read failed"), feedsSeq: readB.seq})
			saved, err := app.store.GetAllFeeds()
			require.NoError(t, err)
			require.Equal(t, "execution 2", saved[0].Title)
			require.Equal(t, saved[0].Title, app.feeds[0].Title)
			require.Equal(t, status, app.statusText)
			require.Equal(t, kind, app.statusKind)
			require.NoError(t, app.err)
			require.False(t, app.spinnerActive)
			require.EqualValues(t, 2, ops.calls.Load(), "reconciliation must not replay mutations")
			require.Equal(t, ViewArticles, app.view)
			require.Same(t, f, app.currentFeed)
			require.Same(t, article, app.currentArticle)
			require.Same(t, article, app.articles[0])
			require.Same(t, article, app.articleList.SelectedItem().(articleItem).article)
			require.EqualValues(t, 7, app.articlesSeq)
			require.Equal(t, "cursor", app.articlesCursor)
			require.True(t, app.articlesHasMore)
		})
	}
}
