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
)

type Model struct {
	view         View
	feeds        []*storage.Feed
	articles     []*storage.Article
	currentFeed  *storage.Feed
	currentArticle *storage.Article
	store        *storage.Store
	width        int
	height       int
	err          error
}

type keyMap struct {
	Up       string
	Down     string
	Left     string
	Right    string
	Enter    string
	Back     string
	Quit     string
	Help     string
	Add      string
	Delete   string
	Refresh  string
	MarkRead string
	OpenMedia string
}

var keys = keyMap{
	Up:       "k",
	Down:     "j",
	Left:     "h",
	Right:    "l",
	Enter:    "enter",
	Back:     "esc",
	Quit:     "q",
	Help:     "?",
	Add:      "a",
	Delete:   "d",
	Refresh:  "r",
	MarkRead: "m",
	OpenMedia: "o",
}