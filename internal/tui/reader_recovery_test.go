package tui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/terminaltext"
	"github.com/stretchr/testify/require"
)

func readerRenderMessage(t *testing.T, cmd tea.Cmd) articleRenderedMsg {
	t.Helper()
	require.NotNil(t, cmd)
	var renders []articleRenderedMsg
	var run func(tea.Cmd)
	run = func(cmd tea.Cmd) {
		switch msg := cmd().(type) {
		case tea.BatchMsg:
			for _, child := range msg {
				run(child)
			}
		case articleRenderedMsg:
			renders = append(renders, msg)
		case spinner.TickMsg:
		default:
			t.Fatalf("unexpected reader follow-up %T", msg)
		}
	}
	run(cmd)
	require.Len(t, renders, 1)
	return renders[0]
}

func prepareReader(t *testing.T, app *App, initial bool) tea.Cmd {
	t.Helper()
	app.currentArticle = &storage.Article{ID: "target", Title: "Target", Content: strings.Repeat("reader body\n\n", 100), MediaURLs: []string{"https://example.test/a", "https://example.test/b"}}
	app.view, app.loadingArticle = ViewReader, initial
	app.viewport.Width, app.viewport.Height = 80, 10
	app.viewport.SetContent(strings.Repeat("previous article\n", 100))
	app.viewport.SetYOffset(12)
	if initial {
		app.startSpinner(MsgLoadingArticle)
	}
	return app.renderArticle(app.currentArticle)
}

func TestReaderRenderCompletesBeneathMediaChooser(t *testing.T) {
	for _, initial := range []bool{false, true} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("initial=%v/error=%v", initial, failed), func(t *testing.T) {
				app := commandTestApp(t)
				cmd := prepareReader(t, app, initial)
				app.keyHandler.openMediaList()
				items := app.mediaList.Items()
				article := app.currentArticle
				msg := cmd().(articleRenderedMsg)
				if failed {
					msg.err = errors.New("render failed beneath overlay")
				}
				wrong := msg
				wrong.articleID = "other"
				app.Update(wrong)
				require.Equal(t, initial, app.loadingArticle)
				stale := msg
				stale.seq--
				app.Update(stale)
				require.Equal(t, initial, app.loadingArticle)
				app.Update(msg)
				require.Equal(t, ViewMedia, app.view)
				require.Equal(t, items, app.mediaList.Items())
				require.False(t, app.loadingArticle)
				require.False(t, app.spinnerActive)
				if failed {
					require.ErrorIs(t, app.err, msg.err)
				} else {
					require.NoError(t, app.err)
					require.NotContains(t, terminaltext.Safe(app.viewport.View()), "previous article")
					if initial {
						require.Contains(t, terminaltext.Safe(app.viewport.View()), "Target")
						require.Zero(t, app.viewport.YOffset)
					} else {
						require.Contains(t, terminaltext.Safe(app.viewport.View()), "reader body")
						require.Equal(t, 12, app.viewport.YOffset)
					}
				}
				_, back := app.keyHandler.navigateBack()
				require.Nil(t, back, "accepted render must not be replayed")
				require.Equal(t, ViewReader, app.view)
				require.Same(t, article, app.currentArticle)
				require.NotContains(t, app.View(), MsgLoadingArticle)
			})
		}
	}
}

func TestReaderSearchSuspendsThenResumesSameTarget(t *testing.T) {
	for _, media := range []bool{false, true} {
		for _, initial := range []bool{false, true} {
			t.Run(fmt.Sprintf("media=%v/initial=%v", media, initial), func(t *testing.T) {
				app := commandTestApp(t)
				old := prepareReader(t, app, initial)
				article := app.currentArticle
				if media {
					app.keyHandler.openMediaList()
				}
				app.keyHandler.enterSearchMode()
				require.Equal(t, ViewSearch, app.view)
				require.False(t, app.loadingArticle)
				require.False(t, app.spinnerActive)
				require.Equal(t, initial, app.readerInitialPending)
				app.keyHandler.enterSearchMode() // must not overwrite the return target
				before := app.viewport.View()
				stale := old().(articleRenderedMsg)
				app.Update(stale)
				stale.err = errors.New("stale render failed")
				app.Update(stale)
				require.NoError(t, app.err)
				require.Equal(t, before, app.viewport.View())
				require.Equal(t, 12, app.viewport.YOffset)
				article.Title = "resumed snapshot"
				_, resume := app.keyHandler.navigateBack()
				if media {
					require.Equal(t, ViewMedia, app.view)
					require.Equal(t, ViewReader, app.mediaPreviousView)
				} else {
					require.Equal(t, ViewReader, app.view)
				}
				require.Equal(t, initial, app.loadingArticle)
				article.Title = "changed after scheduling"
				app.Update(stale)
				require.Equal(t, initial, app.loadingArticle, "stale error must not release resumed load")
				msg := readerRenderMessage(t, resume)
				require.Contains(t, terminaltext.Safe(msg.content), "resumed snapshot")
				require.NotContains(t, terminaltext.Safe(msg.content), "changed after")
				app.Update(msg)
				require.NoError(t, app.err)
				require.False(t, app.loadingArticle)
				require.False(t, app.spinnerActive)
				require.False(t, app.readerInitialPending)
				if initial {
					require.Zero(t, app.viewport.YOffset)
				} else {
					require.Equal(t, 12, app.viewport.YOffset)
				}
				if media {
					app.keyHandler.navigateBack()
				}
				require.Equal(t, ViewReader, app.view)
				require.Same(t, article, app.currentArticle)
				if initial {
					require.Contains(t, terminaltext.Safe(app.viewport.View()), "resumed snapshot")
				} else {
					require.Contains(t, terminaltext.Safe(app.viewport.View()), "reader body")
				}
				require.NotContains(t, terminaltext.Safe(app.viewport.View()), "previous article")
			})
		}
	}
}

func TestSearchResultReaderMediaNestedSearchReturnsToOriginalView(t *testing.T) {
	app := commandTestApp(t)
	app.keyHandler.enterSearchMode() // feeds -> search
	article := &storage.Article{ID: "result", Title: "Result", Content: "body", MediaURLs: []string{"https://example.test/a", "https://example.test/b"}}
	app.keyHandler.selectSearchResult(searchResultItem{isArticle: true, article: article})
	app.Update(articleRenderedMsg{articleID: article.ID, seq: app.renderSeq, content: "result content"})
	app.keyHandler.openMediaList()
	require.Equal(t, ViewFeeds, app.previousView, "media must not overwrite the search origin")
	app.keyHandler.enterSearchMode() // media -> nested search
	_, resume := app.keyHandler.navigateBack()
	require.Equal(t, ViewMedia, app.view)
	app.Update(readerRenderMessage(t, resume))
	app.keyHandler.navigateBack()
	require.Equal(t, ViewReader, app.view)
	app.keyHandler.navigateBack()
	require.Equal(t, ViewSearch, app.view)
	app.keyHandler.navigateBack()
	require.Equal(t, ViewFeeds, app.view)
}

func TestReaderNavigationAndCompletionPreserveOtherSpinner(t *testing.T) {
	app := commandTestApp(t)
	prepareReader(t, app, true)
	app.startSpinner(MsgRefreshing)
	app.keyHandler.enterSearchMode()
	require.True(t, app.spinnerActive)
	require.Equal(t, MsgRefreshing, app.spinnerLabel)
	_, resume := app.keyHandler.navigateBack()
	require.True(t, app.loadingArticle)
	require.Equal(t, MsgRefreshing, app.spinnerLabel)
	app.Update(readerRenderMessage(t, resume))
	require.False(t, app.loadingArticle)
	require.True(t, app.spinnerActive)
	require.Equal(t, MsgRefreshing, app.spinnerLabel)
	app.keyHandler.navigateBack()
	require.Equal(t, MsgRefreshing, app.spinnerLabel)
}

func TestSuspendedReaderShutdownRejectsCompletionAndCommands(t *testing.T) {
	app := commandTestApp(t)
	old := prepareReader(t, app, true)()
	app.keyHandler.enterSearchMode()
	_, resume := app.keyHandler.navigateBack()
	app.Close()
	_, followup := app.Update(old)
	require.Nil(t, followup)
	msg := resume()
	// A batch may have already been assembled; every worker child must
	// reject execution after shutdown, without touching the renderer/store.
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, child := range batch {
			switch result := child().(type) {
			case nil, spinner.TickMsg:
			default:
				t.Fatalf("command ran after Close: %T", result)
			}
		}
	} else {
		require.Nil(t, msg)
	}
}
