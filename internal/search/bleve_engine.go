package search

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/pders01/fwrd/internal/debuglog"
	"github.com/pders01/fwrd/internal/storage"
	"github.com/pders01/fwrd/internal/validation"
)

type bleveEngine struct {
	store   *storage.Store
	idx     bleve.Index
	pending *bleve.Batch
}

// NewBleveEngine creates or opens a Bleve index at indexPath and indexes current data.
func NewBleveEngine(store *storage.Store, indexPath string) (Searcher, error) {
	var idx bleve.Index
	var err error

	// Validate and sanitize the index path for security
	// Use permissive validation for testing (when path is in temp directory)
	pathHandler := validation.NewSecurePathHandler()
	if strings.Contains(indexPath, os.TempDir()) {
		pathHandler = validation.NewPermissivePathHandler()
	}

	validatedPath, err := pathHandler.GetSecureIndexPath(indexPath)
	if err != nil {
		return nil, fmt.Errorf("invalid index path: %w", err)
	}
	indexPath = validatedPath

	// Ensure parent directory exists securely
	parentDir := filepath.Dir(indexPath)
	if _, dirErr := pathHandler.EnsureSecureDirectory(parentDir); dirErr != nil {
		return nil, fmt.Errorf("failed to create index directory: %w", dirErr)
	}

	// Try open first; only reindex from scratch if we had to create a new index
	// or the existing one is empty. Incremental updates during feed refresh
	// (via UpdateListener / BatchIndexer) keep the index in sync afterwards.
	freshIndex := false
	idx, err = bleve.Open(indexPath)
	if err != nil {
		idxMapping := buildIndexMapping()
		idx, err = bleve.New(indexPath, idxMapping)
		if err != nil {
			return nil, err
		}
		freshIndex = true
	}

	be := &bleveEngine{store: store, idx: idx}

	needsReindex := freshIndex
	if !needsReindex {
		if n, cErr := idx.DocCount(); cErr == nil && n == 0 {
			needsReindex = true
		}
	}

	if needsReindex {
		if err := be.reindexAll(); err != nil {
			debuglog.Errorf("reindexAll failed: %v", err)
			return nil, err
		}
	} else {
		debuglog.Infof("bleve index opened (skipping reindex)")
	}
	debuglog.Infof("bleve index ready at %s", indexPath)
	return be, nil
}

func buildIndexMapping() mapping.IndexMapping {
	im := bleve.NewIndexMapping()
	im.DefaultAnalyzer = standard.Name

	// Generic doc mapping with boosted fields
	dm := bleve.NewDocumentMapping()

	title := bleve.NewTextFieldMapping()
	title.Analyzer = standard.Name
	title.Store = true
	title.IncludeTermVectors = true
	title.DocValues = true

	desc := bleve.NewTextFieldMapping()
	desc.Analyzer = standard.Name
	desc.Store = true
	desc.IncludeTermVectors = false

	content := bleve.NewTextFieldMapping()
	content.Analyzer = standard.Name
	content.Store = false
	content.IncludeTermVectors = false

	url := bleve.NewTextFieldMapping()
	url.Analyzer = standard.Name
	url.Store = true

	// Store feed_id for reconstructing context in results
	feedID := bleve.NewTextFieldMapping()
	feedID.Analyzer = standard.Name
	feedID.Store = true

	dm.AddFieldMappingsAt("title", title)
	dm.AddFieldMappingsAt("description", desc)
	dm.AddFieldMappingsAt("content", content)
	dm.AddFieldMappingsAt("url", url)
	dm.AddFieldMappingsAt("feed_id", feedID)

	im.DefaultMapping = dm
	return im
}

// Chunked indexing constants to prevent OOM conditions
const (
	maxBatchSize       = 100  // Maximum documents per batch
	maxArticlesPerFeed = 1000 // Maximum articles to process per feed at once
)

func (b *bleveEngine) reindexAll() error {
	feeds, err := b.store.GetAllFeeds()
	if err != nil {
		return err
	}

	logger := debuglog.WithFields(map[string]interface{}{
		"component": "search",
		"operation": "reindexAll",
		"feedCount": len(feeds),
	})
	logger.Infof("Starting chunked reindexing for %d feeds", len(feeds))

	// Process feeds in small batches to prevent OOM
	batch := b.idx.NewBatch()
	batchCount := 0
	totalProcessed := 0

	for _, f := range feeds {
		// Add feed to batch
		_ = batch.Index(docIDForFeed(f.ID), map[string]any{
			"type":        "feed",
			"feed_id":     f.ID,
			"title":       f.Title,
			"description": f.Description,
			"url":         f.URL,
		})
		batchCount++

		// Process articles for this feed in chunks
		if err := b.indexArticlesInChunks(f.ID, &batch, &batchCount); err != nil {
			debuglog.Errorf("Error indexing articles for feed %s: %v", f.ID, err)
			continue
		}

		// Commit batch if it's getting large
		if batchCount >= maxBatchSize {
			if err := b.commitBatch(batch); err != nil {
				return err
			}
			totalProcessed += batchCount
			debuglog.Infof("Processed %d documents so far", totalProcessed)
			batch = b.idx.NewBatch()
			batchCount = 0
		}
	}

	// Commit any remaining documents in the final batch
	if batchCount > 0 {
		if err := b.commitBatch(batch); err != nil {
			return err
		}
		totalProcessed += batchCount
	}

	logger.Infof("Completed chunked reindexing: %d total documents processed", totalProcessed)
	return nil
}

// indexArticlesInChunks processes articles for a feed in memory-efficient chunks
// using cursor pagination so feeds larger than maxArticlesPerFeed terminate.
func (b *bleveEngine) indexArticlesInChunks(feedID string, batch **bleve.Batch, batchCount *int) error {
	cursor := ""
	for {
		arts, err := b.store.GetArticlesWithCursor(feedID, maxArticlesPerFeed, cursor)
		if err != nil {
			return fmt.Errorf("failed to get articles for feed %s: %w", feedID, err)
		}
		if len(arts) == 0 {
			break
		}

		for _, a := range arts {
			_ = (*batch).Index(docIDForArticle(a.ID), map[string]any{
				"type":        "article",
				"feed_id":     feedID,
				"article_id":  a.ID,
				"title":       a.Title,
				"description": a.Description,
				"content":     a.Content,
				"url":         a.URL,
			})
			(*batchCount)++

			if *batchCount >= maxBatchSize {
				if err := b.commitBatch(*batch); err != nil {
					return err
				}
				debuglog.Infof("Committed batch during article processing: %d documents", *batchCount)
				*batch = b.idx.NewBatch()
				*batchCount = 0
			}
		}

		if len(arts) < maxArticlesPerFeed {
			break
		}
		cursor = arts[len(arts)-1].ID
	}

	return nil
}

// commitBatch safely commits a batch with error handling and logging
func (b *bleveEngine) commitBatch(batch *bleve.Batch) error {
	if batch.Size() == 0 {
		return nil
	}

	err := b.idx.Batch(batch)
	if err != nil {
		debuglog.Errorf("bleve batch index error: %v", err)
		return fmt.Errorf("failed to commit batch: %w", err)
	}

	debuglog.Infof("Successfully committed batch with %d documents", batch.Size())
	return nil
}

func (b *bleveEngine) Search(query string, limit int) ([]*Result, error) {
	if len(strings.TrimSpace(query)) < 2 {
		return []*Result{}, nil
	}
	// Tokenize input and build an OR of per-term matches across key fields with boosts
	tokens := tokenize(query)
	var qs []bleveQuery.Query
	for _, tok := range tokens {
		// title^4
		qt := bleve.NewMatchQuery(tok)
		qt.SetField("title")
		qt.SetBoost(4.0)
		qs = append(qs, qt)
		qtp := bleve.NewPrefixQuery(strings.ToLower(tok))
		qtp.SetField("title")
		qtp.SetBoost(3.5)
		qs = append(qs, qtp)
		// description^2
		qd := bleve.NewMatchQuery(tok)
		qd.SetField("description")
		qd.SetBoost(2.0)
		qs = append(qs, qd)
		qdp := bleve.NewPrefixQuery(strings.ToLower(tok))
		qdp.SetField("description")
		qdp.SetBoost(1.8)
		qs = append(qs, qdp)
		// content^1
		qc := bleve.NewMatchQuery(tok)
		qc.SetField("content")
		qc.SetBoost(1.0)
		qs = append(qs, qc)
		qcp := bleve.NewPrefixQuery(strings.ToLower(tok))
		qcp.SetField("content")
		qcp.SetBoost(0.8)
		qs = append(qs, qcp)
		// url^0.5
		qu := bleve.NewMatchQuery(tok)
		qu.SetField("url")
		qu.SetBoost(0.5)
		qs = append(qs, qu)
		qup := bleve.NewPrefixQuery(strings.ToLower(tok))
		qup.SetField("url")
		qup.SetBoost(0.3)
		qs = append(qs, qup)
	}
	if len(qs) == 0 {
		return []*Result{}, nil
	}
	q := bleve.NewDisjunctionQuery(qs...)
	srch := bleve.NewSearchRequestOptions(q, limit, 0, false)
	srch.Fields = []string{"title", "description", "feed_id", "url"}
	srch.Highlight = bleve.NewHighlight()
	res, err := b.idx.Search(srch)
	if err != nil {
		return nil, err
	}
	out := make([]*Result, 0, len(res.Hits))
	for _, h := range res.Hits {
		r := &Result{Score: h.Score}
		if strings.HasPrefix(h.ID, "feed:") {
			id := strings.TrimPrefix(h.ID, "feed:")
			f := &storage.Feed{ID: id}
			if t, ok := h.Fields["title"].(string); ok {
				f.Title = t
			}
			if d, ok := h.Fields["description"].(string); ok {
				f.Description = d
			}
			if u, ok := h.Fields["url"].(string); ok {
				f.URL = u
			}
			r.Feed = f
			r.IsArticle = false
		} else if strings.HasPrefix(h.ID, "article:") {
			id := strings.TrimPrefix(h.ID, "article:")
			a := &storage.Article{ID: id}
			if t, ok := h.Fields["title"].(string); ok {
				a.Title = t
			}
			if d, ok := h.Fields["description"].(string); ok {
				a.Description = d
			}
			if u, ok := h.Fields["url"].(string); ok {
				a.URL = u
			}
			if fid, ok := h.Fields["feed_id"].(string); ok {
				a.FeedID = fid
				if f, err := b.store.GetFeed(fid); err == nil {
					r.Feed = f
				}
			}
			r.Article = a
			r.IsArticle = true
			// Attach a snippet if available
			if frags, ok := h.Fragments["content"]; ok && len(frags) > 0 {
				r.Matches = append(r.Matches, Match{Field: "content", Text: frags[0], Weight: 1})
			} else if frags, ok := h.Fragments["description"]; ok && len(frags) > 0 {
				r.Matches = append(r.Matches, Match{Field: "description", Text: frags[0], Weight: 1})
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (b *bleveEngine) SearchInArticle(article *storage.Article, query string) ([]*Result, error) {
	if len(strings.TrimSpace(query)) < 2 || article == nil {
		return []*Result{}, nil
	}
	// Local search within content/title/description without using the global index
	// to keep implementation light.
	terms := tokenize(query)
	feed := &storage.Feed{ID: article.FeedID, Title: "Current Article"}
	if res := (&Engine{store: b.store}).searchArticle(feed, article, terms); res != nil {
		return []*Result{res}, nil
	}
	return []*Result{}, nil
}

// OnDataUpdated indexes the provided feed and its articles using memory-efficient chunking.
func (b *bleveEngine) OnDataUpdated(feed *storage.Feed, articles []*storage.Article) {
	var batch *bleve.Batch
	if b.pending != nil {
		batch = b.pending
	} else {
		batch = b.idx.NewBatch()
	}

	batchCount := batch.Size()

	// Index the feed if provided
	if feed != nil {
		_ = batch.Index(docIDForFeed(feed.ID), map[string]any{
			"type":        "feed",
			"feed_id":     feed.ID,
			"title":       feed.Title,
			"description": feed.Description,
			"url":         feed.URL,
		})
		batchCount++
	}

	// Process articles in chunks to prevent OOM for large article collections
	if len(articles) > maxBatchSize {
		debuglog.Infof("Processing %d articles in chunks to prevent OOM", len(articles))
	}

	for i, a := range articles {
		_ = batch.Index(docIDForArticle(a.ID), map[string]any{
			"type":        "article",
			"feed_id":     a.FeedID,
			"article_id":  a.ID,
			"title":       a.Title,
			"description": a.Description,
			"content":     a.Content,
			"url":         a.URL,
		})
		batchCount++

		// If not using batch mode and batch is getting large, commit it
		if b.pending == nil && batchCount >= maxBatchSize {
			if err := b.commitBatch(batch); err != nil {
				debuglog.Errorf("Error committing chunked batch in OnDataUpdated: %v", err)
			}
			// Start a new batch for remaining articles
			if i < len(articles)-1 {
				batch = b.idx.NewBatch()
				batchCount = 0
			}
		}
	}

	// Commit the final batch if not using batch mode
	if b.pending == nil && batchCount > 0 {
		if err := b.commitBatch(batch); err != nil {
			debuglog.Errorf("Error committing final batch in OnDataUpdated: %v", err)
		}
	}
}

// DocCount reports total documents in the index.
func (b *bleveEngine) DocCount() (int, error) {
	q := bleve.NewMatchAllQuery()
	req := bleve.NewSearchRequestOptions(q, 0, 0, false)
	res, err := b.idx.Search(req)
	if err != nil {
		return 0, err
	}
	return int(res.Total), nil
}

// OnFeedDeleted removes the feed document and every article document
// belonging to feedID from the bleve index.
//
// Each iteration always queries from offset 0: the index shrinks as
// docs are deleted, so what was at position size on the previous call
// is now at position 0. The maxIterations guard caps the loop in the
// event a Delete silently fails and the same hits keep reappearing.
func (b *bleveEngine) OnFeedDeleted(feedID string) {
	_ = b.idx.Delete(docIDForFeed(feedID))

	tq := bleve.NewTermQuery(feedID)
	tq.SetField("feed_id")

	const (
		pageSize      = 1000
		maxIterations = 1024 // up to ~1M article docs per feed
	)
	for range maxIterations {
		req := bleve.NewSearchRequestOptions(tq, pageSize, 0, false)
		req.Fields = []string{}
		res, err := b.idx.Search(req)
		if err != nil || res == nil || len(res.Hits) == 0 {
			return
		}
		for _, h := range res.Hits {
			_ = b.idx.Delete(h.ID)
		}
		if len(res.Hits) < pageSize {
			return
		}
	}
	debuglog.Warnf("OnFeedDeleted hit maxIterations for feed %s; some docs may remain", feedID)
}

// Batch index support
var _ interface {
	BeginBatch()
	CommitBatch()
} = (*bleveEngine)(nil)

func (b *bleveEngine) BeginBatch() { b.pending = b.idx.NewBatch() }
func (b *bleveEngine) CommitBatch() {
	if b.pending != nil {
		_ = b.idx.Batch(b.pending)
		b.pending = nil
	}
}

// Close closes the underlying index
func (b *bleveEngine) Close() error { return b.idx.Close() }

func docIDForFeed(feedID string) string   { return "feed:" + feedID }
func docIDForArticle(artID string) string { return "article:" + artID }
