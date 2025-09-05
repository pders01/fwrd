package tui

import (
	"github.com/pders01/fwrd/internal/storage"
)

type View int

const (
	ViewFeeds View = iota
	ViewArticles
	ViewReader
	ViewAddFeed
	ViewDeleteConfirm
	ViewSearch
	ViewMedia
)

type Model struct {
	view           View
	feeds          []*storage.Feed
	articles       []*storage.Article
	currentFeed    *storage.Feed
	currentArticle *storage.Article
	store          *storage.Store
	width          int
	height         int
	err            error
}
