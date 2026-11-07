package tui

import (
	"errors"
	"html"
	"strings"
	"testing"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/stretchr/testify/require"
)

func TestUntrustedListTextIsSanitizedBeforeStyling(t *testing.T) {
	attack := "safe\x1b]52;c;clipboardsecret\a\x1b[2J\x00\u009b31m text"
	feed := &storage.Feed{Title: attack, Description: attack, URL: "https://example.test/" + attack}
	article := &storage.Article{Title: attack, Description: attack, Read: true}
	require.Equal(t, "safe text", (feedItem{feed: feed}).Title())
	require.Equal(t, "safe text", (feedItem{feed: feed}).Description())
	require.Equal(t, ReadItemStyle.Render("safe text"), (articleItem{article: article}).Title(), "sanitize input, not the ANSI produced by app styles")
	items := []interface {
		Title() string
		Description() string
		FilterValue() string
	}{
		feedItem{feed: feed}, articleItem{article: article},
		searchResultItem{feed: feed}, searchResultItem{feed: feed, article: article, isArticle: true},
		mediaItem{url: feed.URL},
	}
	for _, item := range items {
		for _, text := range []string{item.Title(), item.Description(), item.FilterValue()} {
			require.NotContains(t, text, "clipboardsecret")
			require.NotContains(t, text, "\x1b]52")
			require.NotContains(t, text, "\x1b[2J")
			require.NotContains(t, text, "\x00")
			require.NotContains(t, text, "\u009b")
		}
	}
	feed.LastError = attack
	require.NotContains(t, (feedItem{feed: feed}).Description(), "clipboardsecret")
}

func TestUntrustedErrorsStatusHeadersAndInputAreSanitized(t *testing.T) {
	app := commandTestApp(t)
	attack := "safe\x1b]52;c;clipboardsecret\a\x1b[2J text"
	app.err = errors.New(attack)
	require.NotContains(t, app.getCustomStatusBar(), "clipboardsecret")
	require.NotContains(t, app.getCustomStatusBar(), "\x1b[2J")
	app.err = nil
	app.setStatus(attack, 0)
	require.NotContains(t, app.getCustomStatusBar(), "clipboardsecret")
	require.NotContains(t, renderHeader(attack, attack, 100), "clipboardsecret")
	for _, view := range []View{ViewAddFeed, ViewRenameFeed, ViewSearch} {
		app.view = view
		app.textInput.Reset()
		app.textInput.Focus()
		app.searchInput.Reset()
		app.searchInput.Focus()
		app.keyHandler.HandleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(attack), Paste: true})
		input := app.textInput.Value()
		if view == ViewSearch {
			input = app.searchInput.Value()
		}
		require.Equal(t, "safe text", input)
	}
}

func TestReaderSanitizesDecodedHTMLControlsAndPreservesUnicode(t *testing.T) {
	md := htmlToMarkdown("<p>日本語 &#27;[2Jsafe &#x9d;52;c;secret&#7; text</p>")
	require.Contains(t, md, "日本語")
	require.NotContains(t, md, "\x1b")
	require.NotContains(t, md, "\u009d")
	require.NotContains(t, md, "secret")
	require.Equal(t, "safe text", sanitizeAndLimitContent("safe\x1b[2J text", maxTitleSize))
	// Small limits and multibyte boundaries must not panic or return bad UTF-8.
	require.True(t, strings.HasSuffix(sanitizeAndLimitContent(strings.Repeat("é", 20), 10), "**[Content truncated due to size limit]**"))
}

func TestReaderRejectsEntityEncodedTerminalSequences(t *testing.T) {
	app := commandTestApp(t)
	article := &storage.Article{ID: "a", Title: "&#27;]52;c;title-secret&#7; title", Content: "before &#27;[2J after &#x1b;]52;c;body-secret&#7; tail", URL: "https://example.test/%1b%5b2J"}
	msg := app.renderArticle(article)().(articleRenderedMsg)
	require.NoError(t, msg.err)
	require.NotContains(t, msg.content, "\x1b[2J")
	require.NotContains(t, msg.content, "\x1b]52")
	require.NotContains(t, msg.content, "title-secret")
	require.NotContains(t, msg.content, "body-secret")
}

func FuzzSafeMarkdownInput(f *testing.F) {
	for _, seed := range []string{"&#27;]52;c;secret&#7;", "&#x1b;[2J", "&amp;#27;[2J", "&#38;#27;[2J", "&#0000000027;[2J", "&#x9d;52;c;secret&#7;", "&&#11;#11", "&#\x0011", "&Tab;&NewLine;", "日本語"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		// A subsequent Markdown entity-decoding pass must never resurrect
		// controls after the input has been sanitized.
		decoded := html.UnescapeString(safeMarkdownInput(input))
		for _, r := range decoded {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				t.Fatalf("entity decoding resurrected %U from %q", r, input)
			}
		}
	})
}

type renameSearch struct {
	commandSearch
	notified *storage.Feed
}

func (s *renameSearch) OnDataUpdated(feed *storage.Feed, _ []*storage.Article) { s.notified = feed }

func TestRenameUsesAtomicFieldsAndCapturedIdentity(t *testing.T) {
	app := commandTestApp(t)
	feed := &storage.Feed{ID: "feed", Title: "old", URL: "https://example.test/old", ETag: "old", DeskID: "old"}
	require.NoError(t, app.store.SaveFeed(feed))
	engine := &renameSearch{}
	app.searchEngine = engine
	app.feedToRename = feed
	cmd := app.renameFeed("  new title  ")
	// Metadata changes after scheduling the rename must survive it. The UI
	// selection may also move before the tea.Cmd is actually executed.
	fresh := *feed
	fresh.URL, fresh.ETag, fresh.DeskID, fresh.LastError = "https://example.test/new", "fresh", "new-desk", "new failure"
	require.NoError(t, app.store.SaveFeed(&fresh))
	feed.ID = "different-selection"
	app.feedToRename = nil
	msg := cmd().(feedRenamedMsg)
	require.NoError(t, msg.err)
	saved, err := app.store.GetFeed("feed")
	require.NoError(t, err)
	require.Equal(t, "new title", saved.Title)
	require.Equal(t, "new title", saved.TitleOverride)
	require.Equal(t, fresh.URL, saved.URL)
	require.Equal(t, fresh.ETag, saved.ETag)
	require.Equal(t, fresh.DeskID, saved.DeskID)
	require.Equal(t, fresh.LastError, saved.LastError)
	require.Equal(t, saved, engine.notified, "index receives committed metadata, not a UI snapshot")
}
