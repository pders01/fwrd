package tui

import (
	"testing"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/storage"
)

func TestKeyHandler_ModifierKey(t *testing.T) {
	cfg := config.TestConfig()
	store := &storage.Store{}
	app := NewApp(store, cfg)

	// Test that keyHandler is initialized with correct modifier
	assert.NotNil(t, app.keyHandler)
	assert.Equal(t, "ctrl+", app.keyHandler.modifierKey)
}

func TestKeyHandler_HandleKey_CtrlN(t *testing.T) {
	cfg := config.TestConfig()
	store := &storage.Store{}
	app := NewApp(store, cfg)

	// Start with ViewFeeds
	app.view = ViewFeeds

	// Send Ctrl+N key
	msg := tea.KeyMsg{Type: tea.KeyCtrlN}
	updatedModel, _ := app.Update(msg)
	updatedApp := updatedModel.(*App)

	// Should switch to ViewAddFeed
	assert.Equal(t, ViewAddFeed, updatedApp.view, "Ctrl+N should switch to ViewAddFeed")
}

func TestKeyHandler_HandleKey_CtrlS(t *testing.T) {
	cfg := config.TestConfig()
	store := &storage.Store{}
	app := NewApp(store, cfg)

	// Start with ViewFeeds
	app.view = ViewFeeds

	// Send Ctrl+S key
	msg := tea.KeyMsg{Type: tea.KeyCtrlS}
	updatedModel, _ := app.Update(msg)
	updatedApp := updatedModel.(*App)

	// Should switch to ViewSearch
	assert.Equal(t, ViewSearch, updatedApp.view, "Ctrl+S should switch to ViewSearch")
}

func TestKeyHandler_HandleKey_CtrlX(t *testing.T) {
	cfg := config.TestConfig()
	store := &storage.Store{}
	app := NewApp(store, cfg)

	// Start with ViewFeeds
	app.view = ViewFeeds

	// Add a feed to delete
	app.feeds = []*storage.Feed{{ID: "test-feed", Title: "Test Feed"}}
	app.feedList.SetItems([]list.Item{feedItem{feed: app.feeds[0]}})

	// Send Ctrl+X key
	msg := tea.KeyMsg{Type: tea.KeyCtrlX}
	updatedModel, _ := app.Update(msg)
	updatedApp := updatedModel.(*App)

	// Should switch to ViewDeleteConfirm
	assert.Equal(t, ViewDeleteConfirm, updatedApp.view, "Ctrl+X should switch to ViewDeleteConfirm")
}
