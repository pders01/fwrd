package feed

import (
	"strings"
	"testing"
)

func TestGUIDLessArticleIdentityIsStable(t *testing.T) {
	const source = `<rss version="2.0"><channel><title>Test</title>
<item><title>Linked</title><link>https://publisher.example/story</link></item>
<item><title>Content only</title><description>First body</description></item>
<item><title>Content only</title><description>Different body</description></item>
</channel></rss>`
	parser := NewParser()
	first, err := parser.Parse(strings.NewReader(source), "feed")
	if err != nil {
		t.Fatal(err)
	}
	second, err := parser.Parse(strings.NewReader(source), "feed")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || len(second) != 3 {
		t.Fatal("expected three articles")
	}
	for i := range first {
		if first[i].ID != second[i].ID {
			t.Fatalf("item %d changed identity across parses", i)
		}
	}
	if first[1].ID == first[2].ID {
		t.Fatal("different content-only articles share an ID")
	}
	changedTitle, err := parser.Parse(strings.NewReader(strings.Replace(source, "Linked", "Changed", 1)), "feed")
	if err != nil {
		t.Fatal(err)
	}
	if changedTitle[0].ID != first[0].ID {
		t.Fatal("URL identity changed after title edit")
	}
	otherFeed, err := parser.Parse(strings.NewReader(source), "other-feed")
	if err != nil {
		t.Fatal(err)
	}
	if otherFeed[0].ID == first[0].ID {
		t.Fatal("article identity was not namespaced to its feed")
	}
}
