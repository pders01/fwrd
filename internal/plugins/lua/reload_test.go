package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pders01/fwrd/internal/plugins"
)

func TestReloadOwnsPathAcrossNameChangesAndCollisions(t *testing.T) {
	dir := t.TempDir()
	pathA, pathB := filepath.Join(dir, "a.lua"), filepath.Join(dir, "b.lua")
	for path, name := range map[string]string{pathA: "first", pathB: "second"} {
		if err := os.WriteFile(path, []byte(strings.Replace(initialPlugin, `name = "watch"`, `name = "`+name+`"`, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	reg := plugins.NewRegistry(time.Second)
	if count, err := LoadAndRegister(reg, dir, Bindings{}); err != nil || count != 2 {
		t.Fatalf("load count=%d err=%v", count, err)
	}
	old := reg.ListPlugins()
	w := &Watcher{reg: reg, bindings: Bindings{}}
	if err := os.WriteFile(pathA, []byte(strings.Replace(updatedPlugin, `name = "watch"`, `name = "second"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	w.handleUpsert(pathA)
	current := reg.ListPlugins()
	if len(current) != 1 || current[0].Name() != "second" || current[0].(*Plugin).Path() != pathA {
		t.Fatalf("path/name collision was not replaced atomically: %v", current)
	}
	for _, p := range old {
		if p.(*Plugin).state != nil {
			t.Fatal("displaced state was not closed")
		}
	}
	w.handleRemove(pathB)
	if len(reg.ListPlugins()) != 1 {
		t.Fatal("removal of displaced path removed its name's new owner")
	}
	w.handleRemove(pathA)
	if len(reg.ListPlugins()) != 0 || current[0].(*Plugin).state != nil {
		t.Fatal("removal did not unregister and close current owner")
	}
}
