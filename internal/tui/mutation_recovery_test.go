package tui

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

type retryFeedOps struct {
	recordingFeedOps
	err error
}

func (o *retryFeedOps) AddFeed(context.Context, string) (*storage.Feed, error) {
	return &storage.Feed{ID: "f", Title: "Added"}, o.err
}
func (o *retryFeedOps) RenameFeed(context.Context, string, string) (*storage.Feed, error) {
	return &storage.Feed{ID: "f", Title: "Renamed"}, o.err
}
func (o *retryFeedOps) DeleteFeed(context.Context, string) error { return o.err }

func TestFeedMutationRetryClearsOnlyMatchingOperationError(t *testing.T) {
	for _, operation := range []string{"add", "rename", "delete"} {
		for _, unrelated := range []bool{false, true} {
			name := operation + "/owned"
			if unrelated {
				name = operation + "/unrelated"
			}
			t.Run(name, func(t *testing.T) {
				app := commandTestApp(t)
				failure := errors.New("mutation failed")
				ops := &retryFeedOps{err: failure}
				app.feedOps = ops
				f := &storage.Feed{ID: "f"}
				app.feedToRename, app.feedToDelete = f, f
				var run func() tea.Cmd
				var owner *error
				switch operation {
				case "add":
					run, owner = func() tea.Cmd { return app.addFeed("https://example.test/rss") }, &app.addErr
				case "rename":
					run, owner = func() tea.Cmd { return app.renameFeed("New title") }, &app.renameErr
				case "delete":
					run, owner = func() tea.Cmd { return app.deleteFeed(f.ID) }, &app.deleteErr
				}
				cmd := run()
				msg := cmd()
				require.NoError(t, app.err, "workers must not mutate model errors")
				app.Update(msg)
				require.ErrorIs(t, app.err, failure)
				require.ErrorIs(t, *owner, failure)
				other := errors.New("another operation failed")
				if unrelated {
					// Use another operation's real error path, not a global clear.
					if operation == "add" {
						app.Update(feedRenamedMsg{err: other})
					} else {
						app.Update(feedAddedMsg{err: other})
					}
				}
				ops.err = nil
				_, reload := app.Update(run()())
				app.Update(refreshMetadataMessage(t, reload))
				require.NoError(t, *owner)
				if unrelated {
					require.ErrorIs(t, app.err, other)
				} else {
					require.NoError(t, app.err)
				}
				require.Equal(t, ViewFeeds, app.view)
			})
		}
	}
}

func TestFormValidationErrorsBelongToTheirMutation(t *testing.T) {
	for _, view := range []View{ViewAddFeed, ViewRenameFeed} {
		t.Run(map[View]string{ViewAddFeed: "add", ViewRenameFeed: "rename"}[view], func(t *testing.T) {
			app := commandTestApp(t)
			app.feedOps = &retryFeedOps{}
			app.view = view
			app.feedToRename = &storage.Feed{ID: "f"}
			if view == ViewAddFeed {
				app.textInput.SetValue("https://[invalid")
			} else {
				app.textInput.SetValue("   ")
			}
			_, invalid := app.keyHandler.handleTextInputEnter()
			require.NotNil(t, invalid)
			app.Update(invalid())
			require.Error(t, app.err)
			if view == ViewAddFeed {
				require.Same(t, app.err, app.addErr)
				app.textInput.SetValue("https://example.test/rss")
			} else {
				require.Same(t, app.err, app.renameErr)
				app.textInput.SetValue("New title")
			}
			_, valid := app.keyHandler.handleTextInputEnter()
			_, reload := app.Update(valid())
			app.Update(refreshMetadataMessage(t, reload))
			require.NoError(t, app.err)
		})
	}
}
