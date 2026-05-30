package web

import (
	"os"
	"testing"

	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/topics"
)

// Benchmarks the real page-render hot paths against a populated database.
// Point them at a copy of a real DB:
//
//	FWRD_BENCH_DB=/tmp/fwrd-bench.db go test ./internal/web -run x \
//	    -bench 'Bench(HandleFeeds|FrontPage|FeedNames|FeedPage)' -benchmem
//
// They skip when FWRD_BENCH_DB is unset so CI stays hermetic.
func benchServer(b *testing.B) *Server {
	b.Helper()
	path := os.Getenv("FWRD_BENCH_DB")
	if path == "" {
		b.Skip("set FWRD_BENCH_DB to a populated DB copy to run perf benchmarks")
	}
	store, err := storage.NewStore(path)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	b.Cleanup(func() { _ = store.Close() })
	srv, err := NewServer(store, nil, nil, nil)
	if err != nil {
		b.Fatalf("new server: %v", err)
	}
	return srv
}

// BenchmarkHandleFeedsCounts measures the /feeds counting path: one
// FeedStats transaction tallying unread/total per feed via index KeyN.
// Compare against storage.BenchmarkCountDecodeAll (the prior decode-all path).
func BenchmarkHandleFeedsCounts(b *testing.B) {
	s := benchServer(b)
	feeds, err := s.store.GetAllFeeds()
	if err != nil {
		b.Fatalf("get feeds: %v", err)
	}
	b.ReportMetric(float64(len(feeds)), "feeds")
	for b.Loop() {
		if _, err := s.store.FeedStats(); err != nil {
			b.Fatalf("feed stats: %v", err)
		}
	}
}

// BenchmarkFrontPage measures the front-page work done per request:
// load the corpus, build feed-name map, run topic clustering.
func BenchmarkFrontPage(b *testing.B) {
	s := benchServer(b)
	for b.Loop() {
		arts, err := s.store.GetArticles("", frontCorpus)
		if err != nil {
			b.Fatalf("get articles: %v", err)
		}
		_ = s.buildFeedNames()
		_ = topics.Build(arts, topicOptions())
	}
}

// BenchmarkFeedNames isolates the all-feeds decode done on every page load.
func BenchmarkFeedNames(b *testing.B) {
	s := benchServer(b)
	for b.Loop() {
		_ = s.buildFeedNames()
	}
}

// BenchmarkFeedPage measures one paginated feed view (limit 50): the cost
// of decode+sort-all-then-trim in getArticlesForFeed.
func BenchmarkFeedPage(b *testing.B) {
	s := benchServer(b)
	feeds, err := s.store.GetAllFeeds()
	if err != nil || len(feeds) == 0 {
		b.Skipf("need feeds: %v", err)
	}
	// Pick the feed with the most articles to expose the worst case.
	stats, err := s.store.FeedStats()
	if err != nil {
		b.Fatalf("feed stats: %v", err)
	}
	var biggest string
	var max int
	for _, f := range feeds {
		if t := stats[f.ID].Total; t > max {
			max, biggest = t, f.ID
		}
	}
	b.ReportMetric(float64(max), "feed_articles")
	for b.Loop() {
		_, err := s.store.GetArticlesWithCursor(biggest, 50, "")
		if err != nil {
			b.Fatalf("get articles: %v", err)
		}
	}
}
