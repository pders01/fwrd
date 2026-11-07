package control

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

func blockingRemoteSession(t *testing.T) (*Session, *blockingOps) {
	t.Helper()
	store, err := storage.NewStore(storage.MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	if saveErr := store.SaveArticles([]*storage.Article{{ID: "article", FeedID: "feed"}}); saveErr != nil {
		t.Fatal(saveErr)
	}
	ep := ipc.NewEndpoint(filepath.Join(t.TempDir(), "session.db"))
	ops := &blockingOps{started: make(chan struct{}), release: make(chan struct{})}
	server, err := NewRPCServer(ep, store, NewMutationCoordinator(ops))
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	t.Cleanup(func() { _ = server.Close(); <-serveDone; _ = store.Close() })
	session, err := openRemote(ep, config.TestConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session, ops
}

func waitSessionCall(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("session call blocked behind unrelated feed operation")
		return nil
	}
}

func TestRemoteSessionReadsAndReaderActionsDoNotWaitForFeedOperation(t *testing.T) {
	session, ops := blockingRemoteSession(t)
	feedDone := make(chan error, 1)
	go func() { _, err := session.Feeds.AddFeed(context.Background(), "https://example.com"); feedDone <- err }()
	select {
	case <-ops.started:
	case <-time.After(2 * time.Second):
		t.Fatal("feed operation did not start")
	}
	readDone := make(chan error, 1)
	go func() {
		if _, err := session.Store.GetAllFeeds(); err != nil {
			readDone <- err
			return
		}
		readDone <- session.Store.MarkArticleRead("article", true)
	}()
	if err := waitSessionCall(t, readDone); err != nil {
		t.Fatal(err)
	}
	article, err := session.Store.GetArticle("article")
	if err != nil || !article.Read {
		t.Fatalf("reader action did not commit: %#v, %v", article, err)
	}
	close(ops.release)
	if err := waitSessionCall(t, feedDone); err != nil {
		t.Fatal(err)
	}
	operations := session.Feeds.(*remoteOps).client
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if operations.IsConnected() || session.client.IsConnected() {
		t.Fatal("session leaked an IPC connection")
	}
}

func TestRemoteSessionCloseInterruptsFeedOperation(t *testing.T) {
	session, ops := blockingRemoteSession(t)
	feedDone := make(chan error, 1)
	go func() { _, err := session.Feeds.AddFeed(context.Background(), "https://example.com"); feedDone <- err }()
	select {
	case <-ops.started:
	case <-time.After(2 * time.Second):
		t.Fatal("feed operation did not start")
	}
	closed := make(chan error, 1)
	go func() { closed <- session.Close() }()
	if err := waitSessionCall(t, closed); err != nil {
		t.Fatal(err)
	}
	if err := waitSessionCall(t, feedDone); err == nil {
		t.Fatal("interrupted feed operation unexpectedly succeeded")
	}
}
