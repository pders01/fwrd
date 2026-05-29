package topics

import (
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func art(id, title, desc string, day int) *storage.Article {
	return &storage.Article{
		ID:          id,
		Title:       title,
		Description: desc,
		Published:   time.Date(2026, 1, day, 0, 0, 0, 0, time.UTC),
	}
}

// newest-first corpus, as the store returns it.
func corpus() []*storage.Article {
	return []*storage.Article{
		art("a1", "Redis persistence internals", "How redis fork and copy-on-write affect latency", 20),
		art("a2", "Transformer attention explained", "The attention mechanism in transformer neural models", 19),
		art("a3", "Scaling Redis clusters", "Sharding a redis cluster for throughput and latency", 18),
		art("a4", "Neural scaling laws", "Transformer model size and neural training compute", 17),
		art("a5", "Redis streams versus kafka", "Using redis streams as a durable log", 16),
		art("a6", "Attention is cheap now", "Efficient transformer attention with neural kernels", 15),
		art("a7", "A note on gardening", "Tomatoes and basil in spring", 14),
	}
}

func TestBuildFormsTopics(t *testing.T) {
	m := Build(corpus(), DefaultOptions())

	if m.Lead == nil || m.Lead.ID != "a1" {
		t.Fatalf("lead = %v, want a1 (newest)", m.Lead)
	}
	if len(m.Topics) < 2 {
		t.Fatalf("expected at least 2 topics, got %d: %+v", len(m.Topics), labels(m))
	}

	// The three redis articles cluster together; the three transformer
	// articles cluster together. We assert by co-membership rather than
	// label, since the seed term (e.g. "neural" vs "transformer") is an
	// emergent choice of the ranking.
	assertTogether(t, m, "a1", "a3", "a5") // redis
	assertTogether(t, m, "a2", "a4", "a6") // transformer/neural
	if topicOf(m, "a1") == topicOf(m, "a2") {
		t.Error("redis and transformer articles should be in different topics")
	}

	// Every article appears in exactly one topic (partition, no dupes).
	seen := map[string]int{}
	for _, tp := range m.Topics {
		for _, a := range tp.Articles {
			seen[a.ID]++
		}
	}
	for _, a := range corpus() {
		if seen[a.ID] != 1 {
			t.Errorf("article %s appears %d times across topics, want 1", a.ID, seen[a.ID])
		}
	}

	// The lone gardening article shares nothing, so it lands in the
	// catch-all section.
	if g := findArticle(m, "a7"); g == nil {
		t.Error("gardening article missing from all topics")
	}
}

func TestSlugsStableAndUnique(t *testing.T) {
	m := Build(corpus(), DefaultOptions())
	seen := map[string]bool{}
	for _, tp := range m.Topics {
		if tp.Slug == "" {
			t.Errorf("topic %q has empty slug", tp.Label)
		}
		if seen[tp.Slug] {
			t.Errorf("duplicate slug %q", tp.Slug)
		}
		seen[tp.Slug] = true
		if m.BySlug(tp.Slug) == nil {
			t.Errorf("BySlug(%q) returned nil", tp.Slug)
		}
	}
	// Determinism: rebuilding yields identical slugs in identical order.
	m2 := Build(corpus(), DefaultOptions())
	if len(m.Topics) != len(m2.Topics) {
		t.Fatalf("non-deterministic topic count: %d vs %d", len(m.Topics), len(m2.Topics))
	}
	for i := range m.Topics {
		if m.Topics[i].Slug != m2.Topics[i].Slug {
			t.Errorf("topic %d slug differs across builds: %q vs %q", i, m.Topics[i].Slug, m2.Topics[i].Slug)
		}
	}
}

func TestBuildEmpty(t *testing.T) {
	m := Build(nil, DefaultOptions())
	if m.Lead != nil || len(m.Topics) != 0 {
		t.Errorf("empty corpus should yield empty model, got lead=%v topics=%d", m.Lead, len(m.Topics))
	}
	if m.BySlug("anything") != nil {
		t.Error("BySlug on empty model should be nil")
	}
}

func labels(m *Model) []string {
	var out []string
	for _, t := range m.Topics {
		out = append(out, t.Label)
	}
	return out
}

// topicOf returns the slug of the topic containing article id, or "" if
// none. Slugs uniquely identify topics, so equal slugs mean same topic.
func topicOf(m *Model, id string) string {
	for _, tp := range m.Topics {
		for _, a := range tp.Articles {
			if a.ID == id {
				return tp.Slug
			}
		}
	}
	return ""
}

func assertTogether(t *testing.T, m *Model, ids ...string) {
	t.Helper()
	first := topicOf(m, ids[0])
	if first == "" {
		t.Fatalf("article %s not in any topic", ids[0])
	}
	for _, id := range ids[1:] {
		if got := topicOf(m, id); got != first {
			t.Errorf("article %s in topic %q, want same as %s (%q)", id, got, ids[0], first)
		}
	}
}

func findArticle(m *Model, id string) *storage.Article {
	for _, tp := range m.Topics {
		for _, a := range tp.Articles {
			if a.ID == id {
				return a
			}
		}
	}
	return nil
}
