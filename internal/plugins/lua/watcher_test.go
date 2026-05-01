package lua

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/plugins"
)

const initialPlugin = `return {
  name = "watch",
  priority = 1,
  can_handle = function() return true end,
  enhance = function(url) return { feed_url = url, title = "v1" } end,
}`

const updatedPlugin = `return {
  name = "watch",
  priority = 1,
  can_handle = function() return true end,
  enhance = function(url) return { feed_url = url, title = "v2" } end,
}`

// waitFor polls cond until it returns true or timeout elapses. Used to
// give fsnotify events a chance to propagate without sleeping forever.
func waitFor(t *testing.T, msg string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func TestWatcherReloadsModifiedPlugin(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.lua")
	if err := os.WriteFile(path, []byte(initialPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := plugins.NewRegistry(time.Second)
	results, err := LoadDir(dir, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Plugin != nil {
			reg.Register(r.Plugin)
		}
	}

	w, err := NewWatcher(reg, dir, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = w.Run(ctx) }()

	if got := titleOf(t, reg, "https://example.com"); got != "v1" {
		t.Fatalf("initial title = %q want v1", got)
	}

	if err := os.WriteFile(path, []byte(updatedPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "title to flip to v2", func() bool {
		return titleOf(t, reg, "https://example.com") == "v2"
	})
}

func TestWatcherUnregistersOnDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.lua")
	if err := os.WriteFile(path, []byte(initialPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := plugins.NewRegistry(time.Second)
	results, err := LoadDir(dir, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Plugin != nil {
			reg.Register(r.Plugin)
		}
	}

	w, err := NewWatcher(reg, dir, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = w.Run(ctx) }()

	if len(reg.ListPlugins()) != 1 {
		t.Fatalf("expected 1 plugin registered, got %d", len(reg.ListPlugins()))
	}

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "plugin to be unregistered", func() bool {
		return len(reg.ListPlugins()) == 0
	})
}

func TestWatcherKeepsOldPluginOnBadReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "watch.lua")
	if err := os.WriteFile(path, []byte(initialPlugin), 0o644); err != nil {
		t.Fatal(err)
	}

	reg := plugins.NewRegistry(time.Second)
	results, _ := LoadDir(dir, Bindings{})
	for _, r := range results {
		if r.Plugin != nil {
			reg.Register(r.Plugin)
		}
	}

	w, err := NewWatcher(reg, dir, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = w.Run(ctx) }()

	if err := os.WriteFile(path, []byte("this is not lua{{{"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Give fsnotify a moment to deliver the event; the plugin should
	// remain registered (a broken save must not orphan a working one).
	time.Sleep(150 * time.Millisecond)
	if got := titleOf(t, reg, "https://example.com"); got != "v1" {
		t.Errorf("bad save displaced working plugin: title=%q", got)
	}
}

func titleOf(t *testing.T, reg *plugins.Registry, url string) string {
	t.Helper()
	info, err := reg.EnhanceFeed(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	return info.Title
}
