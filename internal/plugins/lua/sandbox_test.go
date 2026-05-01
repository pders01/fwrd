package lua

import (
	"strings"
	"testing"

	gluapkg "github.com/yuin/gopher-lua"
)

func TestSandboxStripsBannedGlobals(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	for _, name := range bannedGlobals {
		if v := L.GetGlobal(name); v != gluapkg.LNil {
			t.Errorf("global %q must be nil after sandboxing, got %v", name, v)
		}
	}

	for _, name := range []string{"io", "os", "package", "debug"} {
		if v := L.GetGlobal(name); v != gluapkg.LNil {
			t.Errorf("global %q must not be exposed, got %v", name, v)
		}
	}
}

func TestSandboxExposesHostBindings(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	for _, name := range []string{"http", "json", "regex", "log"} {
		if v := L.GetGlobal(name); v == gluapkg.LNil {
			t.Errorf("global %q must be exposed", name)
		}
	}
}

func TestSandboxAllowsSafeStdlib(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	if err := L.DoString(`assert(string.upper("ab") == "AB")
assert(table.concat({"a","b"}, ",") == "a,b")
assert(math.floor(1.7) == 1)`); err != nil {
		t.Fatalf("safe stdlib unexpectedly failed: %v", err)
	}
}

func TestSandboxBlocksLoadString(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	err := L.DoString(`local f = loadstring("return 1") return f()`)
	if err == nil {
		t.Fatal("loadstring must not be callable in sandbox")
	}
	if !strings.Contains(err.Error(), "attempt to call") && !strings.Contains(err.Error(), "nil") {
		t.Logf("got expected error: %v", err)
	}
}
