package editorial

import (
	"slices"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/storage"
)

func TestBuildDeterministicDiverseEdition(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	feeds := []*storage.Feed{
		{ID: "wire", URL: "https://wire.example/rss", DeskID: DeskWorld},
		{ID: "daily", URL: "https://daily.example/rss", DeskID: DeskWorld},
		{ID: "public", URL: "https://public.example/rss", DeskID: DeskWorld},
		{ID: "tech", URL: "https://tech.example/rss", DeskID: DeskTechnology},
	}
	articles := []*storage.Article{
		article("newest", "tech", "A new compiler arrives", now.Add(-time.Hour)),
		article("e1", "wire", "Election court reviews disputed ballots", now.Add(-2*time.Hour)),
		article("e2", "daily", "Election officials answer court challenge", now.Add(-3*time.Hour)),
		article("e3", "public", "Election result heads to court", now.Add(-4*time.Hour)),
		article("solo1", "tech", "SQLite query planner notes", now.Add(-5*time.Hour)),
		article("solo2", "tech", "SQLite release details", now.Add(-6*time.Hour)),
		article("solo3", "tech", "SQLite extension guide", now.Add(-7*time.Hour)),
		article("future", "wire", "Election tomorrow", now.Add(time.Hour)),
		article("undated", "daily", "Election archive", time.Time{}),
	}
	opts := DefaultOptions()
	opts.Now = now
	first := Build(articles, feeds, opts)
	reversed := slices.Clone(articles)
	slices.Reverse(reversed)
	second := Build(reversed, feeds, opts)

	if got, want := deskIDs(first), deskIDs(second); !slices.Equal(got, want) {
		t.Fatalf("desk order depends on insertion: %v vs %v", got, want)
	}
	if !slices.Equal(deskIDs(first), []string{
		DeskWorld, DeskPoliticsSociety, DeskBusiness, DeskCulture,
		DeskScienceHealth, DeskClimate, DeskFoodTravel, DeskSport,
		DeskTechnology, DeskUnfiled,
	}) {
		t.Fatalf("unexpected stable desk order: %v", deskIDs(first))
	}
	world := first.ByDesk(DeskWorld)
	if world == nil || len(world.Topics) == 0 {
		t.Fatal("multi-source election fixture did not produce a promoted World topic")
	}
	for _, topic := range world.Topics {
		if topic.SourceCount < 2 {
			t.Errorf("promoted topic %q has %d source", topic.Label, topic.SourceCount)
		}
	}
	for _, topic := range first.ByDesk(DeskTechnology).Topics {
		if topic.Label == "Sqlite" {
			t.Fatal("single-source SQLite run was promoted")
		}
	}
	if first.Lead == nil || first.Lead.ID == "newest" {
		t.Fatalf("lead = %#v; expected supported story to outrank raw newest", first.Lead)
	}
	if first.Lead.ID == "future" || first.Lead.ID == "undated" {
		t.Fatalf("invalidly dated story selected as lead: %s", first.Lead.ID)
	}
	if first.LeadScore.TopicSources < 2 {
		t.Fatalf("lead score does not expose topic support: %+v", first.LeadScore)
	}
	if second.Lead == nil || second.Lead.ID != first.Lead.ID {
		t.Fatalf("lead depends on insertion: %v vs %v", first.Lead, second.Lead)
	}
}

func TestBuildAppliesDocumentedSourceCaps(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	feeds := []*storage.Feed{
		{ID: "loud-world", URL: "https://loud.example/world", DeskID: DeskWorld},
		{ID: "loud-tech", URL: "https://loud.example/tech", DeskID: DeskTechnology},
		{ID: "other", URL: "https://other.example/rss", DeskID: DeskWorld},
	}
	var articles []*storage.Article
	for i := range 10 {
		articles = append(articles, article("lw"+string(rune('a'+i)), "loud-world", "World dispatch", now.Add(-time.Duration(i)*time.Minute)))
		articles = append(articles, article("lt"+string(rune('a'+i)), "loud-tech", "Technology dispatch", now.Add(-time.Duration(i)*time.Minute-time.Second)))
		articles = append(articles, article("o"+string(rune('a'+i)), "other", "Other dispatch", now.Add(-time.Duration(i)*time.Minute-2*time.Second)))
	}
	opts := DefaultOptions()
	opts.Now = now
	opts.MaxArticles = 12
	opts.MaxPerSource = 3
	opts.MaxPerSourceInDesk = 2
	opts.MaxPerDesk = 8
	edition := Build(articles, feeds, opts)

	for _, source := range edition.Diagnostics.Sources {
		if source.Count > opts.MaxPerSource {
			t.Errorf("source %s has %d stories, cap %d", source.ID, source.Count, opts.MaxPerSource)
		}
	}
	for _, section := range edition.Desks {
		counts := map[string]int{}
		for _, a := range section.Articles {
			var feed *storage.Feed
			for _, candidate := range feeds {
				if candidate.ID == a.FeedID {
					feed = candidate
				}
			}
			counts[sourceKey(feed, a.FeedID)]++
		}
		for source, count := range counts {
			if count > opts.MaxPerSourceInDesk {
				t.Errorf("source %s has %d in desk %s, cap %d", source, count, section.Desk.ID, opts.MaxPerSourceInDesk)
			}
		}
	}
}

func article(id, feedID, title string, published time.Time) *storage.Article {
	return &storage.Article{ID: id, FeedID: feedID, Title: title, Published: published}
}

func deskIDs(edition *Edition) []string {
	ids := make([]string, len(edition.Desks))
	for i, desk := range edition.Desks {
		ids[i] = desk.Desk.ID
	}
	return ids
}
