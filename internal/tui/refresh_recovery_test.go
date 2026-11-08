package tui

import (
	"context"
	"errors"
	"testing"

	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestRefreshRecoveryClearsOnlyItsOwnError(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "refresh_owned", true: "unrelated"}[unrelated], func(t *testing.T) {
			app := commandTestApp(t)
			failure := errors.New("RPC unavailable")
			app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
				return feed.RefreshSummary{}, failure
			}}
			app.Update(app.refreshFeeds()())
			require.ErrorIs(t, app.err, failure)
			other := errors.New("unrelated reader failure")
			if unrelated {
				app.err = other
			}
			app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
				return feed.RefreshSummary{UpdatedFeeds: 1}, nil
			}}
			_, reload := app.Update(app.refreshFeeds()())
			app.Update(refreshMetadataMessage(t, reload))
			require.NoError(t, app.refreshErr)
			if unrelated {
				require.ErrorIs(t, app.err, other)
			} else {
				require.NoError(t, app.err, "recovered refresh status must not stay hidden by its old error")
			}
		})
	}
}

func TestRefreshMetadataReloadRejectsStaleSuccessAndError(t *testing.T) {
	app := commandTestApp(t)
	f := &storage.Feed{ID: "f", Title: "Before"}
	require.NoError(t, app.store.SaveFeed(f))
	old := app.loadFeeds()().(feedsLoadedMsg)
	f.Title = "After"
	require.NoError(t, app.store.SaveFeed(f))
	fresh := app.loadFeeds()().(feedsLoadedMsg)
	app.Update(fresh)
	app.view, app.loadingArticle = ViewReader, true
	app.startSpinner(MsgLoadingArticle)
	app.Update(old)
	app.Update(errorMsg{err: errors.New("stale metadata error"), feedsSeq: old.seq})
	require.Equal(t, "After", app.feeds[0].Title)
	require.NoError(t, app.err)
	require.True(t, app.loadingArticle)
	require.True(t, app.spinnerActive)
	require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
}

func TestNewRefreshInvalidatesEarlierMetadataReload(t *testing.T) {
	app := commandTestApp(t)
	old := app.loadFeeds()().(feedsLoadedMsg)
	app.feedOps = &refreshResultOps{refresh: func(context.Context, bool) (feed.RefreshSummary, error) {
		return feed.RefreshSummary{UpdatedFeeds: 1}, nil
	}}
	refresh := app.refreshFeeds()
	app.startSpinner(MsgRefreshing)
	app.Update(old)
	app.Update(errorMsg{err: errors.New("previous reload failed"), feedsSeq: old.seq})
	require.NoError(t, app.err)
	require.True(t, app.spinnerActive)
	_, reload := app.Update(refresh())
	app.Update(refreshMetadataMessage(t, reload))
	require.NoError(t, app.err)
	require.False(t, app.spinnerActive)
}

func TestMetadataReadFailurePreservesReaderAndRecoversIndependently(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata_owned", true: "unrelated"}[unrelated], func(t *testing.T) {
			app := commandTestApp(t)
			app.loadFeeds() // reserve the identity of the current metadata read
			app.view, app.loadingArticle = ViewReader, true
			app.startSpinner(MsgLoadingArticle)
			failure := errors.New("metadata RPC unavailable")
			app.Update(errorMsg{err: failure, feedsSeq: app.feedsSeq})
			require.ErrorIs(t, app.err, failure)
			require.True(t, app.loadingArticle)
			require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
			other := errors.New("reader failed")
			if unrelated {
				app.err = other
			}
			app.Update(app.loadFeeds()())
			require.NoError(t, app.feedsErr)
			if unrelated {
				require.ErrorIs(t, app.err, other)
			} else {
				require.NoError(t, app.err)
			}
			require.True(t, app.loadingArticle)
			require.Equal(t, MsgLoadingArticle, app.spinnerLabel)
		})
	}
}
