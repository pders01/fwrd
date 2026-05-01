package lua

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const goodPluginScript = `return {
  name = "test",
  priority = 42,
  can_handle = function(url) return string.find(url, "example.com") ~= nil end,
  enhance = function(url)
    return {
      feed_url = url .. "/rss",
      title = "Test - " .. url,
      description = "test",
      metadata = { kind = "test" },
    }
  end,
}`

func writePlugin(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write plugin: %v", err)
	}
	return path
}

func TestLoadDirMixesValidAndInvalid(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "good.lua", goodPluginScript)
	writePlugin(t, dir, "bad_syntax.lua", "this is not lua{{{")
	writePlugin(t, dir, "bad_shape.lua", `return 42`)
	writePlugin(t, dir, "missing_fn.lua", `return { name = "x" }`)
	writePlugin(t, dir, "ignored.txt", `not loaded`)

	results, err := LoadDir(dir, Bindings{})
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("expected 4 results (1 ok + 3 errs), got %d", len(results))
	}

	var ok, fail int
	for _, r := range results {
		if r.Err == nil && r.Plugin != nil {
			ok++
			defer r.Plugin.Close()
		} else {
			fail++
		}
	}
	if ok != 1 || fail != 3 {
		t.Errorf("ok=%d fail=%d, want 1/3", ok, fail)
	}
}

func TestLoadDirMissing(t *testing.T) {
	results, err := LoadDir(filepath.Join(t.TempDir(), "does-not-exist"), Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	if results != nil {
		t.Fatalf("expected nil for missing dir, got %v", results)
	}
}

func TestLoadFileEnforcesSizeCap(t *testing.T) {
	dir := t.TempDir()
	huge := strings.Repeat("-- pad\n", int(MaxScriptSize/7)+1)
	path := writePlugin(t, dir, "huge.lua", huge)

	if _, err := LoadFile(path, Bindings{}); err == nil {
		t.Fatal("expected size-cap error")
	}
}

func TestLuaPluginEnhance(t *testing.T) {
	dir := t.TempDir()
	path := writePlugin(t, dir, "good.lua", goodPluginScript)

	plugin, err := LoadFile(path, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close()

	if !plugin.CanHandle("https://example.com/feed") {
		t.Fatal("CanHandle should be true for example.com")
	}
	if plugin.CanHandle("https://other.com") {
		t.Fatal("CanHandle should be false for non-matching url")
	}

	info, err := plugin.EnhanceFeed(context.Background(), "https://example.com/x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if info.FeedURL != "https://example.com/x/rss" {
		t.Errorf("feed url: %q", info.FeedURL)
	}
	if info.Metadata["kind"] != "test" {
		t.Errorf("custom metadata lost: %v", info.Metadata)
	}
	if info.Metadata["plugin"] != "test" {
		t.Errorf("plugin metadata not stamped: %v", info.Metadata)
	}
}

func TestLuaPluginConcurrentSafe(t *testing.T) {
	dir := t.TempDir()
	path := writePlugin(t, dir, "good.lua", goodPluginScript)

	plugin, err := LoadFile(path, Bindings{})
	if err != nil {
		t.Fatal(err)
	}
	defer plugin.Close()

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for range N {
		go func() {
			defer wg.Done()
			if _, err := plugin.EnhanceFeed(context.Background(), "https://example.com/concurrent", nil); err != nil {
				t.Errorf("concurrent enhance: %v", err)
			}
		}()
	}
	wg.Wait()
}
