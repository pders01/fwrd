package feed

import (
	"fmt"
	"io"
	"regexp"
	"time"

	"github.com/mmcdole/gofeed"
	"github.com/pders01/fwrd/internal/storage"
)

type Parser struct {
	parser *gofeed.Parser
}

func NewParser() *Parser {
	return &Parser{
		parser: gofeed.NewParser(),
	}
}

func (p *Parser) Parse(reader io.Reader, feedID string) ([]*storage.Article, error) {
	feed, err := p.parser.Parse(reader)
	if err != nil {
		return nil, fmt.Errorf("parsing feed: %w", err)
	}

	articles := make([]*storage.Article, 0, len(feed.Items))
	for _, item := range feed.Items {
		article := &storage.Article{
			ID:          generateID(feedID, item.GUID),
			FeedID:      feedID,
			Title:       item.Title,
			Description: item.Description,
			Content:     getContent(item),
			URL:         item.Link,
			MediaURLs:   extractMediaURLs(item),
		}

		if item.PublishedParsed != nil {
			article.Published = *item.PublishedParsed
		}

		if item.UpdatedParsed != nil {
			article.Updated = *item.UpdatedParsed
		}

		articles = append(articles, article)
	}

	return articles, nil
}

func getContent(item *gofeed.Item) string {
	if item.Content != "" {
		return item.Content
	}
	return item.Description
}

func extractMediaURLs(item *gofeed.Item) []string {
	var urls []string

	for _, enclosure := range item.Enclosures {
		if enclosure.URL != "" {
			urls = append(urls, enclosure.URL)
		}
	}

	if item.Image != nil && item.Image.URL != "" {
		urls = append(urls, item.Image.URL)
	}

	content := item.Content + " " + item.Description
	urls = append(urls, findMediaInHTML(content)...)

	return uniqueStrings(urls)
}

func findMediaInHTML(html string) []string {
	var urls []string

	imgRegex := regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)
	for _, match := range imgRegex.FindAllStringSubmatch(html, -1) {
		if len(match) > 1 {
			urls = append(urls, match[1])
		}
	}

	videoRegex := regexp.MustCompile(`<video[^>]+src=["']([^"']+)["']`)
	for _, match := range videoRegex.FindAllStringSubmatch(html, -1) {
		if len(match) > 1 {
			urls = append(urls, match[1])
		}
	}

	return urls
}

func generateID(feedID, guid string) string {
	if guid != "" {
		return fmt.Sprintf("%s:%s", feedID, guid)
	}
	return fmt.Sprintf("%s:%d", feedID, time.Now().UnixNano())
}

func uniqueStrings(strs []string) []string {
	seen := make(map[string]bool)
	result := []string{}
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}
