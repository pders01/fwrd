package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

type uncertainFeedOps struct {
	recordingFeedOps
	store *storage.Store
	err   error
	calls int
}

func (o *uncertainFeedOps) AddFeed(context.Context, string) (*storage.Feed, error) {
	o.calls++
	f := &storage.Feed{ID: "added", Title: "Committed addition", LastError: "fetch failed after commit"}
	if err := o.store.SaveFeed(f); err != nil {
		return nil, err
	}
	if err := o.store.SaveArticles([]*storage.Article{{ID: "new-article", FeedID: f.ID}}); err != nil {
		return nil, err
	}
	return nil, o.err
}
func (o *uncertainFeedOps) RenameFeed(_ context.Context, id, title string) (*storage.Feed, error) {
	o.calls++
	if _, err := o.store.RenameFeed(id, title); err != nil {
		return nil, err
	}
	return nil, o.err
}
func (o *uncertainFeedOps) DeleteFeed(_ context.Context, id string) error {
	o.calls++
	if err := o.store.DeleteFeed(id); err != nil {
		return err
	}
	return o.err
}

func TestUncertainFeedMutationsReconcileWithoutReplayOrErrorLoss(t *testing.T) {
	for _, operation := range []string{"add", "rename", "delete"} {
		for _, unrelated := range []bool{false, true} {
			name := operation + "/original-error"
			if unrelated {
				name = operation + "/unrelated-error"
			}
			t.Run(name, func(t *testing.T) {
				a := commandTestApp(t)
				f := &storage.Feed{ID: "target", Title: "Before", LastError: "old failure"}
				keep := &storage.Feed{ID: "keep", Title: "Unrelated feed", LastError: "unrelated badge"}
				require.NoError(t, a.store.SaveFeed(f))
				require.NoError(t, a.store.SaveFeed(keep))
				require.NoError(t, a.store.SaveArticles([]*storage.Article{{ID: "existing", FeedID: f.ID}}))
				a.Update(a.loadFeeds()())
				for i, item := range a.feedList.Items() {
					if item.(feedItem).feed.ID == keep.ID {
						a.feedList.Select(i)
					}
				}
				a.currentFeed = keep
				a.textInput.SetValue("user input must survive")
				failure := errors.New("RPC reply lost after commit")
				ops := &uncertainFeedOps{store: a.store, err: failure}
				a.feedOps = ops
				var cmd tea.Cmd
				var form View
				switch operation {
				case "add":
					form = ViewAddFeed
					cmd = a.addFeed("https://example.test/rss")
				case "rename":
					form = ViewRenameFeed
					a.feedToRename = f
					cmd = a.renameFeed("Committed rename")
				case "delete":
					form = ViewDeleteConfirm
					a.feedToDelete = f
					cmd = a.deleteFeed(f.ID)
				}
				a.view = form
				_, reload := a.Update(cmd())
				require.ErrorIs(t, a.err, failure)
				require.Equal(t, form, a.view, "an error reply cannot close a form")
				other := errors.New("unrelated error received during reconciliation")
				if unrelated {
					a.Update(errorMsg{err: other})
				}
				a.Update(refreshMetadataMessage(t, reload))
				require.Equal(t, 1, ops.calls, "metadata reads must not retry mutations")
				if unrelated {
					require.ErrorIs(t, a.err, other)
				} else {
					require.ErrorIs(t, a.err, failure)
				}
				require.Same(t, keep, a.currentFeed)
				require.Equal(t, keep.ID, a.feedList.SelectedItem().(feedItem).feed.ID)
				require.Equal(t, "user input must survive", a.textInput.Value())
				byID := map[string]*storage.Feed{}
				for _, item := range a.feedList.Items() {
					byID[item.(feedItem).feed.ID] = item.(feedItem).feed
				}
				require.Equal(t, "unrelated badge", byID[keep.ID].LastError)
				stats, err := a.store.FeedStats()
				require.NoError(t, err)
				switch operation {
				case "add":
					require.Equal(t, ViewAddFeed, a.view)
					require.Len(t, byID, 3)
					require.Equal(t, "Committed addition", byID["added"].Title)
					require.Contains(t, (feedItem{feed: byID["added"]}).Title(), "fetch failed")
					require.Equal(t, storage.FeedStat{Unread: 1, Total: 1}, stats["added"])
					require.ErrorIs(t, a.addErr, failure)
				case "rename":
					require.Equal(t, ViewRenameFeed, a.view)
					require.Same(t, f, a.feedToRename)
					require.Len(t, byID, 2)
					require.Equal(t, "Committed rename", byID[f.ID].Title)
					require.Equal(t, "old failure", byID[f.ID].LastError)
					require.Equal(t, storage.FeedStat{Unread: 1, Total: 1}, stats[f.ID])
					require.ErrorIs(t, a.renameErr, failure)
				case "delete":
					require.Equal(t, ViewFeeds, a.view)
					require.Nil(t, a.feedToDelete, "confirmation cannot retain a missing target")
					require.Len(t, byID, 1)
					require.NotContains(t, stats, f.ID)
					require.ErrorIs(t, a.deleteErr, failure)
				}
			})
		}
	}
}

func TestFailedDeleteRetainsConfirmationWhenTargetStillExists(t *testing.T) {
	a := commandTestApp(t)
	f := &storage.Feed{ID: "still-present"}
	require.NoError(t, a.store.SaveFeed(f))
	a.view, a.feedToDelete = ViewDeleteConfirm, f
	a.feedOps = &retryFeedOps{err: errors.New("delete did not commit")}
	_, reload := a.Update(a.deleteFeed(f.ID)())
	a.Update(refreshMetadataMessage(t, reload))
	require.Equal(t, ViewDeleteConfirm, a.view)
	require.Same(t, f, a.feedToDelete)
	require.Error(t, a.err)
}

func TestMutationReconciliationReadFailurePreservesErrorsAndConfirmation(t *testing.T) {
	for _, unrelated := range []bool{false, true} {
		t.Run(map[bool]string{false: "mutation-error", true: "unrelated-error"}[unrelated], func(t *testing.T) {
			a := commandTestApp(t)
			f := &storage.Feed{ID: "deleted"}
			require.NoError(t, a.store.SaveFeed(f))
			a.view, a.feedToDelete = ViewDeleteConfirm, f
			failure := errors.New("delete reply lost")
			ops := &uncertainFeedOps{store: a.store, err: failure}
			a.feedOps = ops
			_, reload := a.Update(a.deleteFeed(f.ID)())
			// A failed authoritative read cannot prove the target is absent.
			expected := a.err
			if unrelated {
				expected = errors.New("new unrelated failure")
				a.err = expected
			}
			readFailure := errors.New("metadata RPC unavailable")
			a.Update(errorMsg{err: readFailure, feedsSeq: a.feedsSeq, preserveError: true})
			require.ErrorIs(t, a.err, expected)
			require.ErrorIs(t, a.deleteErr, failure)
			require.ErrorIs(t, a.feedsErr, readFailure)
			require.Equal(t, ViewDeleteConfirm, a.view)
			require.Same(t, f, a.feedToDelete)
			// Deliver a later successful authoritative read, not a mutation retry.
			stale := refreshMetadataMessage(t, reload)
			recovery := a.reconcileFeeds()
			a.Update(stale)
			require.Same(t, f, a.feedToDelete, "stale reads cannot retire a confirmation")
			a.Update(refreshMetadataMessage(t, recovery))
			require.Nil(t, a.feedToDelete)
			require.Equal(t, ViewFeeds, a.view)
			require.ErrorIs(t, a.err, expected)
			require.NoError(t, a.feedsErr)
			require.Equal(t, 1, ops.calls)
		})
	}
}

func TestMutationValidationDoesNotReloadOrRetry(t *testing.T) {
	for _, view := range []View{ViewAddFeed, ViewRenameFeed} {
		a := commandTestApp(t)
		a.view = view
		a.feedToRename = &storage.Feed{ID: "f"}
		if view == ViewAddFeed {
			a.textInput.SetValue("https://[invalid")
		} else {
			a.textInput.SetValue("  ")
		}
		_, cmd := a.keyHandler.handleTextInputEnter()
		seq := a.feedsSeq
		_, reload := a.Update(cmd())
		require.Nil(t, reload)
		require.Equal(t, seq, a.feedsSeq)
		require.Equal(t, view, a.view)
		require.Error(t, a.err)
	}
}
