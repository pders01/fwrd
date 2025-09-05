package search

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/pders01/fwrd/internal/storage"
)

func TestNewEngine(t *testing.T) {
	store := &storage.Store{}
	engine := NewEngine(store)
	assert.NotNil(t, engine)
	assert.Equal(t, store, engine.store)
}

func TestSearchMinLength(t *testing.T) {
	store := &storage.Store{}
	engine := NewEngine(store)

	tests := []struct {
		name  string
		query string
	}{
		{
			name:  "Empty query",
			query: "",
		},
		{
			name:  "Single character query",
			query: "a",
		},
		{
			name:  "Whitespace only",
			query: "   ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := engine.Search(tt.query, 10)
			assert.NoError(t, err)
			assert.NotNil(t, results)
			assert.Equal(t, 0, len(results), "short queries should return empty results")
		})
	}
}

func TestResultStructure(t *testing.T) {
	// Test that Result has the expected fields
	result := &Result{
		Article:   &storage.Article{},
		Feed:      &storage.Feed{},
		IsArticle: true,
		Score:     0.95,
		Matches:   []Match{},
	}

	assert.NotNil(t, result.Article)
	assert.NotNil(t, result.Feed)
	assert.True(t, result.IsArticle)
	assert.Equal(t, 0.95, result.Score)
	assert.NotNil(t, result.Matches)
}

func TestMatchStructure(t *testing.T) {
	match := Match{
		Field:  "title",
		Text:   "matched text",
		Weight: 1.0,
	}

	assert.Equal(t, "title", match.Field)
	assert.Equal(t, "matched text", match.Text)
	assert.Equal(t, 1.0, match.Weight)
}

func TestSearchInArticle(t *testing.T) {
	store := &storage.Store{}
	engine := NewEngine(store)

	article := &storage.Article{
		ID:      "test-1",
		Title:   "Test Article",
		Content: "This is test content",
	}

	tests := []struct {
		name        string
		query       string
		expectEmpty bool
	}{
		{
			name:        "Empty query",
			query:       "",
			expectEmpty: true,
		},
		{
			name:        "Short query",
			query:       "a",
			expectEmpty: true,
		},
		{
			name:        "Valid query",
			query:       "test",
			expectEmpty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := engine.SearchInArticle(article, tt.query)
			assert.NoError(t, err)
			assert.NotNil(t, results)

			if tt.expectEmpty {
				assert.Equal(t, 0, len(results))
			}
		})
	}
}

func TestSearchInArticleNilArticle(t *testing.T) {
	store := &storage.Store{}
	engine := NewEngine(store)

	results, err := engine.SearchInArticle(nil, "test query")
	assert.NoError(t, err)
	assert.NotNil(t, results)
	assert.Equal(t, 0, len(results))
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "simple words",
			input:    "hello world",
			expected: []string{"hello", "world"},
		},
		{
			name:     "with punctuation",
			input:    "hello, world! test.",
			expected: []string{"hello", "world", "test"},
		},
		{
			name:     "with numbers",
			input:    "test123 456hello",
			expected: []string{"test123", "456hello"},
		},
		{
			name:     "mixed case",
			input:    "Hello WORLD Test",
			expected: []string{"hello", "world", "test"},
		},
		{
			name:     "single characters filtered",
			input:    "a b test c d word",
			expected: []string{"test", "word"},
		},
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:     "special characters",
			input:    "test@email.com hello-world",
			expected: []string{"test", "email", "com", "hello", "world"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenize(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		maxLen   int
		expected string
	}{
		{
			name:     "text shorter than limit",
			text:     "short",
			maxLen:   10,
			expected: "short",
		},
		{
			name:     "text exactly at limit",
			text:     "exactlyten",
			maxLen:   10,
			expected: "exactlyten",
		},
		{
			name:     "text longer than limit",
			text:     "this is a very long text",
			maxLen:   10,
			expected: "this is a…",
		},
		{
			name:     "empty text",
			text:     "",
			maxLen:   10,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncate(tt.text, tt.maxLen)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateRecencyBoost(t *testing.T) {
	// Test the recency boost function
	boost := calculateRecencyBoost("2024-01-01")
	assert.Equal(t, 0.05, boost)

	boost = calculateRecencyBoost(nil)
	assert.Equal(t, 0.05, boost)
}

func TestScoreField(t *testing.T) {
	engine := NewEngine(&storage.Store{})

	tests := []struct {
		name     string
		text     string
		terms    []string
		weight   float64
		minScore float64
	}{
		{
			name:     "exact match",
			text:     "hello world",
			terms:    []string{"hello"},
			weight:   1.0,
			minScore: 2.0, // Should get points for exact match
		},
		{
			name:     "partial match",
			text:     "hello world",
			terms:    []string{"hel"},
			weight:   1.0,
			minScore: 1.0,
		},
		{
			name:     "no match",
			text:     "hello world",
			terms:    []string{"xyz"},
			weight:   1.0,
			minScore: 0,
		},
		{
			name:     "empty text",
			text:     "",
			terms:    []string{"hello"},
			weight:   1.0,
			minScore: 0,
		},
		{
			name:     "multiple terms",
			text:     "hello world test",
			terms:    []string{"hello", "test"},
			weight:   1.0,
			minScore: 4.0, // Should get boost for multiple matches
		},
		{
			name:     "case insensitive",
			text:     "HELLO WORLD",
			terms:    []string{"hello"},
			weight:   1.0,
			minScore: 2.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := engine.scoreField(tt.text, tt.terms, tt.weight)
			assert.GreaterOrEqual(t, score, tt.minScore)
		})
	}
}

func TestFindBestSnippet(t *testing.T) {
	engine := NewEngine(&storage.Store{})

	tests := []struct {
		name      string
		text      string
		terms     []string
		maxLength int
		contains  string
	}{
		{
			name:      "find term in text",
			text:      "This is a long text with the word hello in the middle and more text after",
			terms:     []string{"hello"},
			maxLength: 50,
			contains:  "hello",
		},
		{
			name:      "empty text",
			text:      "",
			terms:     []string{"hello"},
			maxLength: 50,
			contains:  "",
		},
		{
			name:      "text shorter than max",
			text:      "short text",
			terms:     []string{"short"},
			maxLength: 100,
			contains:  "short text",
		},
		{
			name:      "multiple terms",
			text:      "The quick brown fox jumps over the lazy dog",
			terms:     []string{"quick", "dog"},
			maxLength: 50,
			contains:  "quick",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snippet := engine.findBestSnippet(tt.text, tt.terms, tt.maxLength)
			if tt.contains != "" {
				assert.Contains(t, snippet, tt.contains)
			} else {
				assert.Equal(t, "", snippet)
			}
			assert.LessOrEqual(t, len(snippet), tt.maxLength)
		})
	}
}

func TestSearchFeed(t *testing.T) {
	engine := NewEngine(&storage.Store{})

	feed := &storage.Feed{
		ID:          "feed1",
		Title:       "Test Feed",
		Description: "This is a test feed description",
		URL:         "https://example.com/feed.xml",
	}

	tests := []struct {
		name        string
		terms       []string
		expectMatch bool
	}{
		{
			name:        "match title",
			terms:       []string{"test"},
			expectMatch: true,
		},
		{
			name:        "match description",
			terms:       []string{"description"},
			expectMatch: true,
		},
		{
			name:        "match URL",
			terms:       []string{"example"},
			expectMatch: true,
		},
		{
			name:        "no match",
			terms:       []string{"nonexistent"},
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.searchFeed(feed, tt.terms)
			if tt.expectMatch {
				assert.NotNil(t, result)
				assert.Equal(t, feed, result.Feed)
				assert.False(t, result.IsArticle)
				assert.Greater(t, result.Score, 0.0)
				assert.NotEmpty(t, result.Matches)
			} else {
				assert.Nil(t, result)
			}
		})
	}
}

func TestSearchArticle(t *testing.T) {
	engine := NewEngine(&storage.Store{})

	feed := &storage.Feed{
		ID:    "feed1",
		Title: "Test Feed",
	}

	article := &storage.Article{
		ID:          "article1",
		Title:       "Test Article Title",
		Description: "Article description with keywords",
		Content:     "This is the full content of the article with many words and test phrases",
	}

	tests := []struct {
		name        string
		terms       []string
		expectMatch bool
	}{
		{
			name:        "match title",
			terms:       []string{"article"},
			expectMatch: true,
		},
		{
			name:        "match description",
			terms:       []string{"keywords"},
			expectMatch: true,
		},
		{
			name:        "match content",
			terms:       []string{"phrases"},
			expectMatch: true,
		},
		{
			name:        "multiple terms",
			terms:       []string{"test", "article"},
			expectMatch: true,
		},
		{
			name:        "no match",
			terms:       []string{"nonexistent"},
			expectMatch: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := engine.searchArticle(feed, article, tt.terms)
			if tt.expectMatch {
				assert.NotNil(t, result)
				assert.Equal(t, article, result.Article)
				assert.Equal(t, feed, result.Feed)
				assert.True(t, result.IsArticle)
				assert.Greater(t, result.Score, 0.0)
				assert.NotEmpty(t, result.Matches)
			} else {
				assert.Nil(t, result)
			}
		})
	}
}
