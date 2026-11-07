package tui

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/pders01/fwrd/internal/storage"
)

// worker prevents late Cmds from using closed resources, and lets Close drain
// running Cmds before releasing the index. Callers snapshot UI inputs before
// wrapping: workers must never read or mutate the Bubble Tea model.
func (a *App) worker(cmd tea.Cmd) tea.Cmd {
	return func() tea.Msg {
		a.workerMu.Lock()
		if a.closed {
			a.workerMu.Unlock()
			return nil
		}
		a.workerWG.Add(1)
		a.workerMu.Unlock()
		defer a.workerWG.Done()
		return cmd()
	}
}

func (a *App) commandContext() context.Context {
	if a.commandCtx != nil {
		return a.commandCtx
	}
	return context.Background()
}

// Each field has an independent write lane per article. Intents are recorded on
// Update, before Cmd execution, so two rapid toggles compose even when neither
// write has completed. A late Cmd cannot overwrite a newer persisted intent.
type articleIntent struct {
	writeMu sync.Mutex
	latest  atomic.Uint64
	desired bool // Update goroutine only
	pending bool // compose against desired only until the latest completion
}

func (a *App) changeArticleState(article *storage.Article, starred, toggle bool) tea.Cmd {
	if article == nil {
		return nil
	}
	intents := &a.readIntents
	initial := article.Read
	if starred {
		intents = &a.starIntents
		initial = article.Starred
	}
	if *intents == nil {
		*intents = make(map[string]*articleIntent)
	}
	id, store := article.ID, a.store
	intent := (*intents)[id]
	if intent == nil {
		intent = &articleIntent{desired: initial}
		(*intents)[id] = intent
	} else if !intent.pending {
		// A later store-backed list/search reload may reflect another
		// client's change. Completed intents must not mask that state.
		intent.desired = initial
	}
	desired := true
	if toggle {
		desired = !intent.desired
	}
	intent.desired = desired
	intent.pending = true
	seq := intent.latest.Add(1)
	return a.worker(func() tea.Msg {
		intent.writeMu.Lock()
		defer intent.writeMu.Unlock()
		if seq != intent.latest.Load() {
			return nil
		}
		var err error
		if starred {
			err = store.MarkArticleStarred(id, desired)
		} else {
			err = store.MarkArticleRead(id, desired)
		}
		state := desired
		if err != nil {
			state = initial
			// A preceding intent may have succeeded even if its completion
			// hasn't reached Update yet. Recover the authoritative state.
			if saved, readErr := store.GetArticle(id); readErr == nil && saved != nil {
				state = saved.Read
				if starred {
					state = saved.Starred
				}
			}
		}
		if starred {
			return articleStarToggledMsg{articleID: id, seq: seq, starred: state, err: err}
		}
		return articleReadToggledMsg{articleID: id, seq: seq, read: state, err: err}
	})
}

func (a *App) applyArticleIntent(id string, seq uint64, state bool, err error, starred bool) {
	intents := a.readIntents
	if starred {
		intents = a.starIntents
	}
	intent := intents[id]
	if intent == nil || seq != intent.latest.Load() {
		return
	}
	intent.desired = state
	intent.pending = false
	if err != nil {
		a.err = err
	}
	apply := func(article *storage.Article) {
		if article != nil && article.ID == id {
			if starred {
				article.Starred = state
			} else {
				article.Read = state
			}
		}
	}
	apply(a.currentArticle)
	for _, article := range a.articles {
		apply(article)
	}
	for _, result := range a.searchResults {
		apply(result.article)
	}
	for _, model := range []*list.Model{&a.articleList, &a.searchList} {
		for _, item := range model.Items() {
			switch item := item.(type) {
			case articleItem:
				apply(item.article)
			case searchResultItem:
				apply(item.article)
			}
		}
	}
}

// snapshotArticle detaches mutable reader flags and slice storage from Cmds.
func snapshotArticle(article *storage.Article) *storage.Article {
	if article == nil {
		return nil
	}
	snapshot := *article
	snapshot.MediaURLs = append([]string(nil), article.MediaURLs...)
	return &snapshot
}
