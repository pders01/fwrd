package search

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/blevesearch/bleve/v2"
	"github.com/blevesearch/bleve/v2/analysis/analyzer/standard"
	"github.com/blevesearch/bleve/v2/mapping"
	bleveQuery "github.com/blevesearch/bleve/v2/search/query"
	"github.com/pders01/fwrd/internal/storage"
)

type bleveEngine struct {
	store *storage.Store
	idx   bleve.Index
}

// NewBleveEngine creates or opens a Bleve index at indexPath and indexes current data.
func NewBleveEngine(store *storage.Store, indexPath string) (Searcher, error) {
	var idx bleve.Index
	var err error

	// Ensure parent directory exists when creating an index
	if mkErr := os.MkdirAll(filepath.Dir(indexPath), 0o755); mkErr != nil {
		// continue; Open/Create below will still error and be returned
		_ = mkErr
	}

	// Try open first
	idx, err = bleve.Open(indexPath)
	if err != nil {
		// Create a new index with simple mapping
		idxMapping := buildIndexMapping()
		idx, err = bleve.New(indexPath, idxMapping)
		if err != nil {
			return nil, err
		}
	}

	be := &bleveEngine{store: store, idx: idx}
	// Initial load
	if err := be.reindexAll(); err != nil {
		return nil, err
	}
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

func (b *bleveEngine) reindexAll() error {
	feeds, err := b.store.GetAllFeeds()
	if err != nil {
		return err
	}

	batch := b.idx.NewBatch()
	for _, f := range feeds {
		_ = batch.Index(docIDForFeed(f.ID), map[string]any{
			"type":        "feed",
			"feed_id":     f.ID,
			"title":       f.Title,
			"description": f.Description,
			"url":         f.URL,
		})

		arts, _ := b.store.GetArticles(f.ID, 0)
		for _, a := range arts {
			_ = batch.Index(docIDForArticle(a.ID), map[string]any{
				"type":        "article",
				"feed_id":     f.ID,
				"article_id":  a.ID,
				"title":       a.Title,
				"description": a.Description,
				"content":     a.Content,
				"url":         a.URL,
			})
		}
	}
	return b.idx.Batch(batch)
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

// OnDataUpdated indexes the provided feed and its articles.
func (b *bleveEngine) OnDataUpdated(feed *storage.Feed, articles []*storage.Article) {
	batch := b.idx.NewBatch()
	if feed != nil {
		_ = batch.Index(docIDForFeed(feed.ID), map[string]any{
			"type":        "feed",
			"feed_id":     feed.ID,
			"title":       feed.Title,
			"description": feed.Description,
			"url":         feed.URL,
		})
	}
	for _, a := range articles {
		_ = batch.Index(docIDForArticle(a.ID), map[string]any{
			"type":        "article",
			"feed_id":     a.FeedID,
			"article_id":  a.ID,
			"title":       a.Title,
			"description": a.Description,
			"content":     a.Content,
			"url":         a.URL,
		})
	}
	_ = b.idx.Batch(batch)
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

// OnFeedDeleted removes all docs for the feed. This simple approach deletes the feed doc only.
// Removing all articles would require iterating; for brevity this only deletes the feed doc.
func (b *bleveEngine) OnFeedDeleted(feedID string) {
	// Delete the feed document
	_ = b.idx.Delete(docIDForFeed(feedID))

	// Delete all article documents for this feed in batches
	// Query: term query on feed_id
	tq := bleve.NewTermQuery(feedID)
	tq.SetField("feed_id")

	from := 0
	size := 1000
	for {
		req := bleve.NewSearchRequestOptions(tq, size, from, false)
		req.Fields = []string{}
		res, err := b.idx.Search(req)
		if err != nil || res == nil || len(res.Hits) == 0 {
			break
		}
		for _, h := range res.Hits {
			_ = b.idx.Delete(h.ID)
		}
		if len(res.Hits) < size {
			break
		}
		from += size
	}
}

func docIDForFeed(feedID string) string   { return "feed:" + feedID }
func docIDForArticle(artID string) string { return "article:" + artID }
