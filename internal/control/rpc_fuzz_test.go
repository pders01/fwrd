package control

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/pders01/fwrd/internal/ipc"
	"github.com/pders01/fwrd/internal/storage"
)

// FuzzRPCDispatchMalformed exercises semantic decoding through the real IPC
// dispatch loop. Every input is followed by a read to prove the database owner
// remains available after malformed parameters.
func FuzzRPCDispatchMalformed(f *testing.F) {
	f.Add([]byte(`null`))
	f.Add([]byte(`[null]`))
	f.Add([]byte(`{"id":"","feed_id":"","limit":-1}`))
	f.Add([]byte(`{"id":123}`))

	dbPath := filepath.Join(f.TempDir(), "dispatch-fuzz.db")
	_, _, endpoint, _ := startTestRPC(f, dbPath)
	client, err := ipc.NewClient(endpoint.Address, endpoint.DatabaseID)
	if err != nil {
		f.Fatal(err)
	}
	defer client.Close()

	methods := []string{"SaveFeed", "SaveArticles", "GetFeed", "GetArticle", "GetArticlesWithCursor", "MarkArticleRead", "ImportFeeds"}
	f.Fuzz(func(t *testing.T, data []byte) {
		var params any
		if unmarshalErr := json.Unmarshal(data, &params); unmarshalErr != nil {
			params = string(data)
		}
		method := methods[0]
		if len(data) > 0 {
			method = methods[int(data[0])%len(methods)]
		}
		_ = client.Call(method, params, nil)

		var stats map[string]storage.FeedStat
		if callErr := client.Call("FeedStats", nil, &stats); callErr != nil {
			t.Fatalf("server unavailable after %s params %q: %v", method, data, callErr)
		}
	})
}
