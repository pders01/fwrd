package storage

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestMergeArticlesReadsStateInsideWriteTransaction(t *testing.T) {
	store, err := NewStore(MemoryPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if saveErr := store.SaveArticles([]*Article{{ID: "article", FeedID: "feed"}}); saveErr != nil {
		t.Fatal(saveErr)
	}

	// Hold the writer lock with an uncommitted reader action. A content merge
	// must wait for this transaction and then read its newly committed flags,
	// not perform a separate read of the older state before acquiring the lock.
	backend := store.backend.(*boltBackend)
	tx, err := backend.db.Begin(true)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	data, err := json.Marshal(&Article{ID: "article", FeedID: "feed", Read: true, Starred: true})
	if err != nil {
		t.Fatal(err)
	}
	if putErr := tx.Bucket(articlesBucket).Put([]byte("article"), data); putErr != nil {
		t.Fatal(putErr)
	}
	if indexErr := setUnreadMembership(tx, "feed", "article", false); indexErr != nil {
		t.Fatal(indexErr)
	}

	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		saved, mergeErr := store.MergeArticles([]*Article{{ID: "article", FeedID: "feed", Title: "Fetched content"}})
		if mergeErr == nil && (len(saved) != 1 || !saved[0].Read || !saved[0].Starred) {
			mergeErr = errors.New("merge did not preserve state committed by preceding writer")
		}
		done <- mergeErr
	}()
	<-started
	if commitErr := tx.Commit(); commitErr != nil {
		t.Fatal(commitErr)
	}
	select {
	case mergeErr := <-done:
		if mergeErr != nil {
			t.Fatal(mergeErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("merge did not finish after writer committed")
	}
	got, err := store.GetArticle("article")
	if err != nil || !got.Read || !got.Starred || got.Title != "Fetched content" {
		t.Fatalf("merged article = %#v, %v", got, err)
	}
}
