package feed

import (
	"strings"
	"testing"

	"github.com/mmcdole/gofeed"
	"github.com/pders01/fwrd/internal/storage"
)

func TestParser_Parse(t *testing.T) {
	parser := NewParser()

	tests := []struct {
		name          string
		feedContent   string
		feedID        string
		expectError   bool
		expectedCount int
		validateFunc  func(t *testing.T, articles []*storage.Article)
	}{
		{
			name:   "valid RSS feed",
			feedID: "test-rss",
			feedContent: `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Test RSS Feed</title>
		<link>http://example.com</link>
		<description>Test Description</description>
		<item>
			<title>First Article</title>
			<link>http://example.com/article1</link>
			<description>This is the first article</description>
			<guid>article-1</guid>
			<pubDate>Wed, 01 Jan 2025 12:00:00 GMT</pubDate>
			<enclosure url="http://example.com/image1.jpg" type="image/jpeg"/>
		</item>
		<item>
			<title>Second Article</title>
			<link>http://example.com/article2</link>
			<description>This is the second article</description>
			<content:encoded><![CDATA[<p>Full content here</p>]]></content:encoded>
			<guid>article-2</guid>
			<pubDate>Thu, 02 Jan 2025 12:00:00 GMT</pubDate>
		</item>
	</channel>
</rss>`,
			expectError:   false,
			expectedCount: 2,
			validateFunc: func(t *testing.T, articles []*storage.Article) {
				if articles[0].Title != "First Article" {
					t.Errorf("expected title 'First Article', got %s", articles[0].Title)
				}
				if articles[0].URL != "http://example.com/article1" {
					t.Errorf("expected URL 'http://example.com/article1', got %s", articles[0].URL)
				}
				if len(articles[0].MediaURLs) != 1 || articles[0].MediaURLs[0] != "http://example.com/image1.jpg" {
					t.Error("expected media URL not found")
				}
				if articles[1].Content != "<p>Full content here</p>" {
					t.Errorf("expected content '<p>Full content here</p>', got %s", articles[1].Content)
				}
			},
		},
		{
			name:   "valid Atom feed",
			feedID: "test-atom",
			feedContent: `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
	<title>Test Atom Feed</title>
	<link href="http://example.org/"/>
	<updated>2025-01-01T12:00:00Z</updated>
	<entry>
		<title>Atom Entry 1</title>
		<link href="http://example.org/entry1"/>
		<id>urn:uuid:1225c695-cfb8-4ebb-aaaa-80da344efa6a</id>
		<updated>2025-01-01T12:00:00Z</updated>
		<summary>Entry summary</summary>
		<content type="html">&lt;p&gt;Entry content&lt;/p&gt;</content>
	</entry>
</feed>`,
			expectError:   false,
			expectedCount: 1,
			validateFunc: func(t *testing.T, articles []*storage.Article) {
				if articles[0].Title != "Atom Entry 1" {
					t.Errorf("expected title 'Atom Entry 1', got %s", articles[0].Title)
				}
				if articles[0].Content != "<p>Entry content</p>" {
					t.Errorf("expected content '<p>Entry content</p>', got %s", articles[0].Content)
				}
			},
		},
		{
			name:   "feed with media in HTML content",
			feedID: "test-media",
			feedContent: `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
	<channel>
		<title>Media Feed</title>
		<item>
			<title>Media Article</title>
			<description><![CDATA[
				Check out this image: <img src="http://example.com/photo.jpg" />
				And this video: <video src="http://example.com/video.mp4"></video>
			]]></description>
			<guid>media-1</guid>
		</item>
	</channel>
</rss>`,
			expectError:   false,
			expectedCount: 1,
			validateFunc: func(t *testing.T, articles []*storage.Article) {
				if len(articles[0].MediaURLs) != 2 {
					t.Errorf("expected 2 media URLs, got %d", len(articles[0].MediaURLs))
				}
				expectedURLs := map[string]bool{
					"http://example.com/photo.jpg": false,
					"http://example.com/video.mp4": false,
				}
				for _, url := range articles[0].MediaURLs {
					expectedURLs[url] = true
				}
				for url, found := range expectedURLs {
					if !found {
						t.Errorf("expected media URL %s not found", url)
					}
				}
			},
		},
		{
			name:          "invalid XML",
			feedID:        "test-invalid",
			feedContent:   "not valid XML",
			expectError:   true,
			expectedCount: 0,
		},
		{
			name:          "empty feed",
			feedID:        "test-empty",
			feedContent:   `<?xml version="1.0" encoding="UTF-8"?><rss version="2.0"><channel></channel></rss>`,
			expectError:   false,
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := strings.NewReader(tt.feedContent)
			articles, err := parser.Parse(reader, tt.feedID)

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if len(articles) != tt.expectedCount {
				t.Errorf("expected %d articles, got %d", tt.expectedCount, len(articles))
			}

			if tt.validateFunc != nil && len(articles) > 0 {
				tt.validateFunc(t, articles)
			}
		})
	}
}

func TestExtractMediaURLs(t *testing.T) {
	tests := []struct {
		name         string
		item         *gofeed.Item
		expectedURLs []string
	}{
		{
			name: "enclosures",
			item: &gofeed.Item{
				Enclosures: []*gofeed.Enclosure{
					{URL: "http://example.com/audio.mp3"},
					{URL: "http://example.com/video.mp4"},
				},
			},
			expectedURLs: []string{"http://example.com/audio.mp3", "http://example.com/video.mp4"},
		},
		{
			name: "image field",
			item: &gofeed.Item{
				Image: &gofeed.Image{
					URL: "http://example.com/image.png",
				},
			},
			expectedURLs: []string{"http://example.com/image.png"},
		},
		{
			name: "HTML content with images",
			item: &gofeed.Item{
				Content: `<p>Article with <img src="http://example.com/img1.jpg" alt="test"> image</p>`,
			},
			expectedURLs: []string{"http://example.com/img1.jpg"},
		},
		{
			name: "duplicate URLs removed",
			item: &gofeed.Item{
				Enclosures: []*gofeed.Enclosure{
					{URL: "http://example.com/media.mp4"},
				},
				Content: `<video src="http://example.com/media.mp4"></video>`,
			},
			expectedURLs: []string{"http://example.com/media.mp4"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urls := extractMediaURLs(tt.item)

			if len(urls) != len(tt.expectedURLs) {
				t.Errorf("expected %d URLs, got %d", len(tt.expectedURLs), len(urls))
			}

			urlMap := make(map[string]bool)
			for _, url := range urls {
				urlMap[url] = true
			}

			for _, expectedURL := range tt.expectedURLs {
				if !urlMap[expectedURL] {
					t.Errorf("expected URL %s not found", expectedURL)
				}
			}
		})
	}
}

func TestGenerateID(t *testing.T) {
	tests := []struct {
		name         string
		feedID       string
		guid         string
		expectPrefix string
	}{
		{
			name:         "with GUID",
			feedID:       "feed123",
			guid:         "article456",
			expectPrefix: "feed123:article456",
		},
		{
			name:         "without GUID",
			feedID:       "feed789",
			guid:         "",
			expectPrefix: "feed789:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id := generateID(tt.feedID, tt.guid)
			if !strings.HasPrefix(id, tt.expectPrefix) {
				t.Errorf("expected ID to start with %s, got %s", tt.expectPrefix, id)
			}
		})
	}
}
