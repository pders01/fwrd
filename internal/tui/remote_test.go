package tui

import (
	"context"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/control"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/storage"
)

type recordingFeedOps struct {
	addedURL     string
	refreshForce bool
	deletedID    string
}

func (o *recordingFeedOps) AddFeed(_ context.Context, url string) (*storage.Feed, error) {
	o.addedURL = url
	return &storage.Feed{ID: "new", Title: "New feed", URL: url}, nil
}

func (o *recordingFeedOps) RefreshFeed(context.Context, string, bool) error { return nil }

func (o *recordingFeedOps) RefreshAllFeeds(_ context.Context, force bool) (feed.RefreshSummary, error) {
	o.refreshForce = force
	return feed.RefreshSummary{UpdatedFeeds: 2, AddedArticles: 3}, nil
}

func (o *recordingFeedOps) DeleteFeed(_ context.Context, id string) error {
	o.deletedID = id
	return nil
}

func (o *recordingFeedOps) ImportFeeds(context.Context, []string) (control.ImportSummary, error) {
	return control.ImportSummary{}, nil
}

func TestRemoteAppRoutesFeedOperationsToSession(t *testing.T) {
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	defer store.Close()

	cfg := &config.Config{}
	ops := &recordingFeedOps{}
	app := newApp(store, cfg, nil, ops, true)
	defer app.Close()

	if app.manager != nil {
		t.Fatal("remote app created a local feed manager")
	}
	if app.searchEngineType != "basic" {
		t.Fatalf("remote search engine = %q, want basic", app.searchEngineType)
	}

	if msg, ok := app.addFeed("example.com")().(feedAddedMsg); !ok || msg.err != nil {
		t.Fatalf("addFeed message = %#v", msg)
	}
	if ops.addedURL != "https://example.com" {
		t.Fatalf("AddFeed URL = %q", ops.addedURL)
	}

	app.SetForceRefresh(true)
	if msg, ok := app.refreshFeeds()().(refreshDoneMsg); !ok || msg.updatedFeeds != 2 || msg.addedArticles != 3 {
		t.Fatalf("refreshFeeds message = %#v", msg)
	}
	if !ops.refreshForce {
		t.Fatal("force-refresh flag was not forwarded")
	}

	if msg, ok := app.deleteFeed("feed-1")().(feedDeletedMsg); !ok || msg.err != nil {
		t.Fatalf("deleteFeed message = %#v", msg)
	}
	if ops.deletedID != "feed-1" {
		t.Fatalf("DeleteFeed ID = %q", ops.deletedID)
	}
}
