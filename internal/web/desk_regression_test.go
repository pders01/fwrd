package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/pders01/fwrd/internal/config"
	"github.com/pders01/fwrd/internal/control"
	"github.com/pders01/fwrd/internal/feed"
	"github.com/pders01/fwrd/internal/opml"
	"github.com/pders01/fwrd/internal/search"
	"github.com/pders01/fwrd/internal/storage"
)

type deskRaceOps struct {
	*control.LocalOps
	deskCalls int
	saveCalls int
}

func (o *deskRaceOps) SaveFeed(_ *storage.Feed) error {
	o.saveCalls++
	return fmt.Errorf("web must not save a stale full feed for desk assignments")
}

func (o *deskRaceOps) SetFeedDesk(id, desk string) (*storage.Feed, error) {
	o.deskCalls++
	// Simulate refreshed metadata committed after the web layer's identity
	// lookup but before its mutation acquires the coordinator lock.
	current, err := o.Store.GetFeed(id)
	if err != nil {
		return nil, err
	}
	current.Title = "Refreshed title"
	current.ETag = "new-etag"
	current.LastError = "new failure state"
	if err := o.Store.SaveFeed(current); err != nil {
		return nil, err
	}
	return o.LocalOps.SetFeedDesk(id, desk)
}

type deskUpdates struct{ feeds []*storage.Feed }

func (*deskUpdates) Search(string, int) ([]*search.Result, error) { return nil, nil }
func (*deskUpdates) SearchInArticle(*storage.Article, string) ([]*search.Result, error) {
	return nil, nil
}

func (l *deskUpdates) OnDataUpdated(f *storage.Feed, _ []*storage.Article) {
	if f != nil {
		snapshot := *f
		l.feeds = append(l.feeds, &snapshot)
	}
}

func TestDeskMutationsUseAtomicOperationAndNotifyCurrentRecord(t *testing.T) {
	for _, restore := range []bool{false, true} {
		t.Run(fmt.Sprintf("OPML=%v", restore), func(t *testing.T) {
			s, store := newTestServer(t)
			id, _ := seed(t, store)
			manager := feed.NewManager(store, config.TestConfig())
			listener := &deskUpdates{}
			manager.RegisterDataListener(listener)
			ops := &deskRaceOps{LocalOps: &control.LocalOps{Store: store, Manager: manager}}
			s.coordinator = control.NewMutationCoordinator(ops)
			wantDesk, wantCalls := "technology", 1
			if restore {
				if _, err := store.SetFeedDesk(id, "world"); err != nil {
					t.Fatal(err)
				}
				// Multiple entries for the same URL must apply in order even
				// when a later desk equals the original snapshot's value.
				wantDesk, wantCalls = "world", 2
				r := httptest.NewRequest(http.MethodPost, "/opml/import", http.NoBody)
				if err := s.applyImportedDeskMetadata(r, []opml.Feed{{URL: "http://example.com/feed", DeskID: "technology"}, {URL: "http://example.com/feed", DeskID: "world"}}); err != nil {
					t.Fatal(err)
				}
			} else {
				response := postForm(t, s.Handler(), "/feeds/"+id+"/desk", url.Values{"desk": {wantDesk}}, true)
				if response.Code != http.StatusSeeOther {
					t.Fatalf("desk response: %d", response.Code)
				}
			}
			if ops.saveCalls != 0 || ops.deskCalls != wantCalls {
				t.Fatalf("SaveFeed calls=%d, SetFeedDesk calls=%d, want 0 and %d", ops.saveCalls, ops.deskCalls, wantCalls)
			}
			current, err := store.GetFeed(id)
			if err != nil || current.DeskID != wantDesk || current.Title != "Refreshed title" || current.ETag != "new-etag" || current.LastError != "new failure state" {
				t.Fatalf("desk assignment clobbered refresh metadata: %+v, %v", current, err)
			}
			if len(listener.feeds) != wantCalls {
				t.Fatalf("search notifications=%d, want %d", len(listener.feeds), wantCalls)
			}
			notified := listener.feeds[len(listener.feeds)-1]
			if notified.DeskID != wantDesk || notified.Title != current.Title || notified.ETag != current.ETag {
				t.Fatalf("notified stale record: %+v", notified)
			}
		})
	}
}

func TestFallbackDeskMutationNotifiesCurrentRecord(t *testing.T) {
	for _, withManager := range []bool{false, true} {
		t.Run(fmt.Sprintf("manager=%v", withManager), func(t *testing.T) {
			s, store := newTestServer(t)
			id, _ := seed(t, store)
			listener := &deskUpdates{}
			s.searcher = listener
			if withManager {
				s.manager = feed.NewManager(store, config.TestConfig())
				s.manager.RegisterDataListener(listener)
			}
			response := postForm(t, s.Handler(), "/feeds/"+id+"/desk", url.Values{"desk": {"technology"}}, true)
			if response.Code != http.StatusSeeOther || len(listener.feeds) != 1 || listener.feeds[0].DeskID != "technology" {
				t.Fatalf("fallback desk response=%d, notifications=%+v", response.Code, listener.feeds)
			}
			postForm(t, s.Handler(), "/feeds/missing/desk", url.Values{"desk": {"world"}}, true)
			if _, err := store.GetFeed("missing"); err == nil || len(listener.feeds) != 1 {
				t.Fatal("missing feed was resurrected or notified")
			}
		})
	}
}
