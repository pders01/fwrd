package feed

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pders01/fwrd/internal/storage"
)

type mergedArticleListener struct{ batches [][]*storage.Article }

func (l *mergedArticleListener) OnDataUpdated(_ *storage.Feed, articles []*storage.Article) {
	l.batches = append(l.batches, articles)
}

func TestRefreshAllCountsOnlyUniqueInsertions(t *testing.T) {
	manager, store := persistenceManager(t)
	listener := &mergedArticleListener{}
	manager.RegisterDataListener(listener)
	var generation atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = fmt.Fprint(w, `<rss version="2.0"><channel><title>Feed</title>`)
		ids := []string{"a", "b", "a"}
		if generation.Load() == 2 {
			ids = append(ids, "c")
		}
		for _, id := range ids {
			_, _ = fmt.Fprintf(w, `<item><title>generation %d</title><guid>%s</guid></item>`, generation.Load()/2, id)
		}
		_, _ = fmt.Fprint(w, `</channel></rss>`)
	}))
	defer server.Close()
	if err := store.SaveFeed(&storage.Feed{ID: "f", URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	var articleID string
	for i, want := range []int{2, 0, 1} {
		generation.Store(int32(i))
		summary, err := manager.RefreshAllFeeds()
		if err != nil || len(summary.Errors) != 0 || summary.AddedArticles != want || summary.UpdatedFeeds != 1 {
			t.Fatalf("refresh %d = %+v, %v; want added=%d", i, summary, err, want)
		}
		if len(listener.batches) != i+1 || len(listener.batches[i]) != 3+i/2 {
			t.Fatalf("refresh %d lost merged listener records: %+v", i, listener.batches)
		}
		for _, article := range listener.batches[i] {
			if article.Title != fmt.Sprintf("generation %d", i/2) {
				t.Fatalf("listener saw stale content: %+v", article)
			}
			if article.ID == articleID && (!article.Read || !article.Starred) {
				t.Fatalf("listener lost reader state: %+v", article)
			}
		}
		if i == 0 {
			articleID = listener.batches[0][0].ID
			if err := store.MarkArticleRead(articleID, true); err != nil {
				t.Fatal(err)
			}
			if err := store.MarkArticleStarred(articleID, true); err != nil {
				t.Fatal(err)
			}
		}
	}
	if requests.Load() != 3 {
		t.Fatalf("fetch replayed: requests=%d", requests.Load())
	}
}

func TestFailedRefreshHealthRecordingDoesNotHideCommitError(t *testing.T) {
	manager, store := persistenceManager(t)
	listener := &mergedArticleListener{}
	manager.RegisterDataListener(listener)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
		_, _ = fmt.Fprint(w, persistenceFeed("a"))
	}))
	defer server.Close()
	if err := store.SaveFeed(&storage.Feed{ID: "f", URL: server.URL}); err != nil {
		t.Fatal(err)
	}
	err := manager.RefreshFeed("f")
	if err == nil || !strings.Contains(err.Error(), "saving refreshed feed:") || !strings.Contains(err.Error(), "database not open") {
		t.Fatalf("commit error hidden by failed health write: %v", err)
	}
	if len(listener.batches) != 0 {
		t.Fatal("failed commit notified listeners")
	}
}
