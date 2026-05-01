package lua

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fsnotify/fsnotify"
	"github.com/pders01/fwrd/internal/plugins"
)

// Watcher subscribes to filesystem events on a plugin directory and
// keeps the registry in sync. Editing a *.lua file reloads it; deleting
// a *.lua file unregisters the plugin it backs. Files that fail to
// load (syntax error, missing fields) leave the previously-registered
// version in place — a bad save must not orphan a working plugin.
//
// Watcher does not start its own goroutine. Callers run Run inside a
// goroutine they own and cancel via the supplied context.
type Watcher struct {
	reg      *plugins.Registry
	dir      string
	bindings Bindings
	fs       *fsnotify.Watcher
}

// NewWatcher constructs a watcher rooted at dir. The directory must
// already exist; callers can use EnsureDefaults to seed it.
func NewWatcher(reg *plugins.Registry, dir string, b Bindings) (*Watcher, error) {
	if reg == nil {
		return nil, errors.New("nil plugin registry")
	}
	if dir == "" {
		return nil, errors.New("empty plugin dir")
	}
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("creating fsnotify watcher: %w", err)
	}
	if err := w.Add(dir); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("watching %s: %w", dir, err)
	}
	return &Watcher{reg: reg, dir: dir, bindings: b, fs: w}, nil
}

// Run blocks until ctx is cancelled or the underlying fsnotify watcher
// closes. It returns ctx.Err() on cancellation and nil on a clean
// channel close.
func (w *Watcher) Run(ctx context.Context) error {
	defer w.fs.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-w.fs.Events:
			if !ok {
				return nil
			}
			if filepath.Ext(ev.Name) != ".lua" {
				continue
			}
			switch {
			case ev.Has(fsnotify.Remove), ev.Has(fsnotify.Rename):
				w.handleRemove(ev.Name)
			case ev.Has(fsnotify.Create), ev.Has(fsnotify.Write):
				w.handleUpsert(ev.Name)
			}
		case err, ok := <-w.fs.Errors:
			if !ok {
				return nil
			}
			w.warn("fsnotify error: %v", err)
		}
	}
}

// Close stops the watcher and releases the underlying fsnotify
// resources. Safe to call after Run returns.
func (w *Watcher) Close() error {
	return w.fs.Close()
}

func (w *Watcher) handleUpsert(path string) {
	plugin, err := LoadFile(path, w.bindings)
	if err != nil {
		w.warn("reload %s: %v", path, err)
		return
	}
	old := w.reg.Replace(plugin)
	closeIfPossible(old)
	w.info("reloaded plugin %s (priority=%d) from %s",
		plugin.Name(), plugin.Priority(), path)
}

func (w *Watcher) handleRemove(path string) {
	for _, p := range w.reg.ListPlugins() {
		lp, ok := p.(*LuaPlugin)
		if !ok || lp.Path() != path {
			continue
		}
		removed := w.reg.Unregister(lp.Name())
		closeIfPossible(removed)
		w.info("unregistered plugin %s (file removed)", lp.Name())
		return
	}
}

func (w *Watcher) info(format string, args ...any) {
	if w.bindings.Logger != nil {
		w.bindings.Logger.Infof(format, args...)
	}
}

func (w *Watcher) warn(format string, args ...any) {
	if w.bindings.Logger != nil {
		w.bindings.Logger.Warnf(format, args...)
	}
}

func closeIfPossible(p plugins.Plugin) {
	if p == nil {
		return
	}
	if c, ok := p.(interface{ Close() }); ok {
		c.Close()
	}
}
