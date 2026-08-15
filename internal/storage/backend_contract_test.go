package storage_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/control"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

type contractStore struct {
	store   *storage.Store
	cleanup func()
}

func localContractStore(t *testing.T) contractStore {
	t.Helper()
	store, err := storage.NewStore(filepath.Join(t.TempDir(), "local.db"))
	if err != nil {
		t.Fatal(err)
	}
	return contractStore{store: store, cleanup: func() { _ = store.Close() }}
}

func rpcContractStore(t *testing.T) contractStore {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "rpc.db")
	serverStore, err := storage.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	manager := feed.NewManager(serverStore, config.TestConfig())
	coordinator := control.NewMutationCoordinator(&control.LocalOps{Store: serverStore, Manager: manager})
	canonical, err := ipc.CanonicalDBPath(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ipc.NewEndpoint(canonical)
	server, err := control.NewRPCServer(endpoint, serverStore, coordinator)
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve() }()
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	store := storage.NewRPCStore(client)
	return contractStore{
		store: store,
		cleanup: func() {
			_ = store.Close()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = server.Shutdown(ctx)
			<-serveDone
			_ = serverStore.Close()
		},
	}
}

func TestStoreBackendContract(t *testing.T) {
	factories := map[string]func(*testing.T) contractStore{
		"bolt": localContractStore,
		"rpc":  rpcContractStore,
	}
	for name, factory := range factories {
		t.Run(name, func(t *testing.T) {
			env := factory(t)
			defer env.cleanup()
			runStoreContract(t, env.store)
		})
	}
}

func runStoreContract(t *testing.T, store *storage.Store) {
	t.Helper()
	feeds, feedsErr := store.GetAllFeeds()
	if feedsErr != nil || len(feeds) != 0 {
		t.Fatalf("empty GetAllFeeds = %#v, %v", feeds, feedsErr)
	}
	if stats, statsErr := store.FeedStats(); statsErr != nil || len(stats) != 0 {
		t.Fatalf("empty FeedStats = %#v, %v", stats, statsErr)
	}

	for _, value := range []*storage.Feed{
		{ID: "b", URL: "https://example.com/b", Title: "Beta"},
		{ID: "a", URL: "https://resolved.example/a", SubscribedURL: "https://example.com/a", SubscribedTitle: "Alpha source", SubscribedAt: time.Unix(123, 0), Title: "alpha", DeskID: "technology"},
	} {
		if saveErr := store.SaveFeed(value); saveErr != nil {
			t.Fatal(saveErr)
		}
	}
	feeds, feedsErr = store.GetAllFeeds()
	if feedsErr != nil || len(feeds) != 2 || feeds[0].ID != "a" || feeds[1].ID != "b" {
		t.Fatalf("sorted feeds = %#v, %v", feeds, feedsErr)
	}
	feedA, getErr := store.GetFeed("a")
	if getErr != nil || feedA.DeskID != "technology" || feedA.SubscriptionURL() != "https://example.com/a" || feedA.SubscribedTitle != "Alpha source" || !feedA.SubscribedAt.Equal(time.Unix(123, 0)) {
		t.Fatalf("GetFeed(a) subscription metadata = %#v, %v", feedA, getErr)
	}
	if _, getErr := store.GetFeed("missing"); getErr == nil {
		t.Fatal("GetFeed(missing) returned nil error")
	}

	now := time.Now().UTC()
	articles := []*storage.Article{
		{ID: "older", FeedID: "a", Title: "Older", Published: now.Add(-time.Hour)},
		{ID: "newer", FeedID: "a", Title: "Newer", Published: now},
	}
	if err := store.SaveArticles(articles); err != nil {
		t.Fatal(err)
	}
	page, err := store.GetArticlesWithCursor("a", 1, "")
	if err != nil || len(page) != 1 || page[0].ID != "newer" {
		t.Fatalf("first page = %#v, %v", page, err)
	}
	page, err = store.GetArticlesWithCursor("a", 1, page[0].ID)
	if err != nil || len(page) != 1 || page[0].ID != "older" {
		t.Fatalf("second page = %#v, %v", page, err)
	}
	if markErr := store.MarkArticleRead("newer", true); markErr != nil {
		t.Fatal(markErr)
	}
	if markErr := store.MarkArticleStarred("newer", true); markErr != nil {
		t.Fatal(markErr)
	}
	article, err := store.GetArticle("newer")
	if err != nil || !article.Read || !article.Starred {
		t.Fatalf("mutated article = %#v, %v", article, err)
	}
	stats, err := store.FeedStats()
	if err != nil || stats["a"].Total != 2 || stats["a"].Unread != 1 {
		t.Fatalf("stats = %#v, %v", stats, err)
	}
	if deleteErr := store.DeleteFeed("a"); deleteErr != nil {
		t.Fatal(deleteErr)
	}
	remaining, err := store.GetArticles("a", 0)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("articles after delete = %#v, %v", remaining, err)
	}
	if store.WriteGen() != 6 {
		t.Fatalf("WriteGen = %d, want 6", store.WriteGen())
	}
}
