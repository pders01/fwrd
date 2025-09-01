package search

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/pders01/fwrd/internal/storage"
)

// SearchResult represents a search match with relevance scoring
type SearchResult struct {
	Feed      *storage.Feed
	Article   *storage.Article
	IsArticle bool
	Score     float64
	Matches   []Match
}

// Match represents where text was found
type Match struct {
	Field   string // "title", "description", "content"
	Text    string // matched text snippet
	Weight  float64
}

// Engine provides intelligent search without heavy indexing
type Engine struct {
	store *storage.Store
}

// NewEngine creates a new search engine
func NewEngine(store *storage.Store) *Engine {
	return &Engine{store: store}
}

// Search performs intelligent search across feeds and articles
func (e *Engine) Search(query string, limit int) ([]*SearchResult, error) {
	if len(strings.TrimSpace(query)) < 2 {
		return []*SearchResult{}, nil
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return []*SearchResult{}, nil
	}

	var results []*SearchResult

	// Get all feeds
	feeds, err := e.store.GetAllFeeds()
	if err != nil {
		return nil, err
	}

	// Search feeds
	for _, feed := range feeds {
		if result := e.searchFeed(feed, terms); result != nil {
			results = append(results, result)
		}

		// Search articles in this feed
		articles, err := e.store.GetArticles(feed.ID, 200) // Get more articles for search
		if err != nil {
			continue
		}

		for _, article := range articles {
			if result := e.searchArticle(feed, article, terms); result != nil {
				results = append(results, result)
			}
		}
	}

	// Sort by relevance score (highest first)
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// Limit results
	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

// SearchInArticle searches within a specific article's content
func (e *Engine) SearchInArticle(article *storage.Article, query string) ([]*SearchResult, error) {
	if len(strings.TrimSpace(query)) < 2 || article == nil {
		return []*SearchResult{}, nil
	}

	terms := tokenize(query)
	if len(terms) == 0 {
		return []*SearchResult{}, nil
	}

	// Create a mock feed for the result
	feed := &storage.Feed{ID: "current", Title: "Current Article"}
	if result := e.searchArticle(feed, article, terms); result != nil {
		return []*SearchResult{result}, nil
	}

	return []*SearchResult{}, nil
}

// searchFeed searches within a feed's metadata
func (e *Engine) searchFeed(feed *storage.Feed, terms []string) *SearchResult {
	var matches []Match
	var totalScore float64

	// Search title (highest weight)
	if titleScore := e.scoreField(feed.Title, terms, 3.0); titleScore > 0 {
		matches = append(matches, Match{
			Field:  "title",
			Text:   feed.Title,
			Weight: titleScore,
		})
		totalScore += titleScore
	}

	// Search description (medium weight)
	if descScore := e.scoreField(feed.Description, terms, 1.5); descScore > 0 {
		matches = append(matches, Match{
			Field:  "description",
			Text:   truncate(feed.Description, 100),
			Weight: descScore,
		})
		totalScore += descScore
	}

	// Search URL (low weight)
	if urlScore := e.scoreField(feed.URL, terms, 0.5); urlScore > 0 {
		matches = append(matches, Match{
			Field:  "url",
			Text:   feed.URL,
			Weight: urlScore,
		})
		totalScore += urlScore
	}

	if totalScore > 0 {
		return &SearchResult{
			Feed:      feed,
			IsArticle: false,
			Score:     totalScore,
			Matches:   matches,
		}
	}

	return nil
}

// searchArticle searches within an article's content
func (e *Engine) searchArticle(feed *storage.Feed, article *storage.Article, terms []string) *SearchResult {
	var matches []Match
	var totalScore float64

	// Search title (highest weight)
	if titleScore := e.scoreField(article.Title, terms, 4.0); titleScore > 0 {
		matches = append(matches, Match{
			Field:  "title",
			Text:   article.Title,
			Weight: titleScore,
		})
		totalScore += titleScore
	}

	// Search description (high weight)
	if descScore := e.scoreField(article.Description, terms, 2.0); descScore > 0 {
		matches = append(matches, Match{
			Field:  "description",
			Text:   truncate(article.Description, 150),
			Weight: descScore,
		})
		totalScore += descScore
	}

	// Search content (medium weight)
	if contentScore := e.scoreField(article.Content, terms, 1.0); contentScore > 0 {
		// Find best snippet from content
		snippet := e.findBestSnippet(article.Content, terms, 200)
		matches = append(matches, Match{
			Field:  "content",
			Text:   snippet,
			Weight: contentScore,
		})
		totalScore += contentScore
	}

	// Boost recent articles slightly
	if !article.Published.IsZero() {
		// Simple recency boost (max 10% bonus for articles from last week)
		// This prevents old articles from dominating just due to length
		recencyBoost := calculateRecencyBoost(article.Published)
		totalScore *= (1.0 + recencyBoost)
	}

	if totalScore > 0 {
		return &SearchResult{
			Feed:      feed,
			Article:   article,
			IsArticle: true,
			Score:     totalScore,
			Matches:   matches,
		}
	}

	return nil
}

// scoreField calculates relevance score for a field
func (e *Engine) scoreField(text string, terms []string, weight float64) float64 {
	if text == "" {
		return 0
	}

	lower := strings.ToLower(text)
	words := tokenize(text)
	
	var score float64
	matchedTerms := 0

	for _, term := range terms {
		termLower := strings.ToLower(term)
		
		// Exact phrase match (highest score)
		if strings.Contains(lower, termLower) {
			score += 2.0
			matchedTerms++
		}
		
		// Word boundary matches (medium score)
		for _, word := range words {
			wordLower := strings.ToLower(word)
			if wordLower == termLower {
				score += 1.5
				matchedTerms++
			} else if strings.HasPrefix(wordLower, termLower) || strings.HasSuffix(wordLower, termLower) {
				score += 1.0
				matchedTerms++
			} else if strings.Contains(wordLower, termLower) {
				score += 0.5
				matchedTerms++
			}
		}
	}

	// Boost score if multiple terms match
	if len(terms) > 1 && matchedTerms > 1 {
		score *= 1.0 + float64(matchedTerms)/float64(len(terms))
	}

	// Apply TF-IDF-like scoring
	tf := float64(matchedTerms) / float64(len(words))
	score *= (1.0 + math.Log(1.0+tf))

	return score * weight
}

// findBestSnippet finds the most relevant text snippet containing search terms
func (e *Engine) findBestSnippet(text string, terms []string, maxLength int) string {
	if text == "" {
		return ""
	}

	words := strings.Fields(text)
	if len(words) == 0 {
		return ""
	}

	bestScore := 0.0
	bestStart := 0
	windowSize := maxLength / 8 // Approximate words in snippet

	if windowSize > len(words) {
		return truncate(text, maxLength)
	}

	// Sliding window to find best snippet
	for i := 0; i <= len(words)-windowSize; i++ {
		windowText := strings.Join(words[i:i+windowSize], " ")
		score := 0.0
		
		for _, term := range terms {
			if strings.Contains(strings.ToLower(windowText), strings.ToLower(term)) {
				score += 1.0
			}
		}
		
		if score > bestScore {
			bestScore = score
			bestStart = i
		}
	}

	snippet := strings.Join(words[bestStart:bestStart+windowSize], " ")
	return truncate(snippet, maxLength)
}

// tokenize breaks text into searchable terms
func tokenize(text string) []string {
	var terms []string
	current := strings.Builder{}
	
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			current.WriteRune(unicode.ToLower(r))
		} else if current.Len() > 0 {
			if term := current.String(); len(term) > 1 { // Skip single chars
				terms = append(terms, term)
			}
			current.Reset()
		}
	}
	
	if current.Len() > 1 {
		terms = append(terms, current.String())
	}
	
	return terms
}

// truncate limits text length with ellipsis
func truncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen-1] + "…"
}

// calculateRecencyBoost gives slight preference to newer articles
func calculateRecencyBoost(published interface{}) float64 {
	// Simple implementation - could be enhanced with actual time comparison
	// For now, return minimal boost to avoid complexity
	return 0.05 // 5% boost for any dated article
}