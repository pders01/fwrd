package lua

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	lua "github.com/yuin/gopher-lua"
)

func loadTestScript(t *testing.T, source string) *Plugin {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.lua")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	p, err := LoadFile(path, Bindings{ExecutionTimeout: 30 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestLoadFileTimesOut(t *testing.T) {
	path := filepath.Join(t.TempDir(), "infinite.lua")
	if err := os.WriteFile(path, []byte("while true do end"), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if p, err := LoadFile(path, Bindings{ExecutionTimeout: 20 * time.Millisecond}); err == nil {
		p.Close()
		t.Fatal("unbounded script load succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("plugin load exceeded timeout")
	}
}

func TestCanHandleTimesOutWithoutPoisoningState(t *testing.T) {
	p := loadTestScript(t, `return {
name="loop", can_handle=function() while true do end end,
enhance=function(url) return {feed_url=url} end,
}`)
	start := time.Now()
	if p.CanHandle("https://feed.example.org") {
		t.Fatal("infinite routing returned true")
	}
	if time.Since(start) > time.Second {
		t.Fatal("routing exceeded timeout")
	}
	if _, err := p.EnhanceFeed(context.Background(), "https://feed.example.org", nil); err != nil {
		t.Fatalf("state unusable after routing timeout: %v", err)
	}
}

func TestPluginAdmissionRespectsContext(t *testing.T) {
	p := loadTestScript(t, `return {
name="busy", can_handle=function() return true end,
enhance=function(url) return {feed_url=url} end,
}`)
	p.gate <- struct{}{} // hold state admission, without running a second goroutine
	defer p.release()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.EnhanceFeed(ctx, "https://feed.example.org", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("enhance admission error = %v", err)
	}
	if p.CanHandleContext(ctx, "https://feed.example.org") {
		t.Fatal("routing admitted a canceled caller")
	}
	if time.Since(start) > time.Second {
		t.Fatal("canceled admission blocked")
	}
}

func TestEnhanceTimesOut(t *testing.T) {
	p := loadTestScript(t, `return {
name="loop", can_handle=function() return true end,
enhance=function() while true do end end,
}`)
	if _, err := p.EnhanceFeed(context.Background(), "https://feed.example.org", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("enhance timeout = %v", err)
	}
}

func TestJSONBudgetsAndCycles(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()
	for name, source := range map[string]string{
		"self cycle":     `local t={}; t.self=t; local s,e=json.encode(t); assert(s==nil and e~=nil)`,
		"mutual cycle":   `local a,b={},{}; a.b=b; b.a=a; local s,e=json.encode(a); assert(s==nil and e~=nil)`,
		"depth":          `local t={}; for i=1,100 do t={t} end; local s,e=json.encode(t); assert(s==nil and e~=nil)`,
		"nodes":          `local t={}; for i=1,100001 do t[i]=true end; local s,e=json.encode(t); assert(s==nil and e~=nil)`,
		"input bytes":    `local s,e=json.parse(string.rep(" ",4194305)); assert(s==nil and e~=nil)`,
		"decode depth":   `local s,e=json.parse(string.rep("[",100).."0"..string.rep("]",100)); assert(s==nil and e~=nil)`,
		"decode nodes":   `local s,e=json.parse("["..string.rep("0,",100001).."0]"); assert(s==nil and e~=nil)`,
		"shared acyclic": `local t={x=1}; local s,e=json.encode({a=t,b=t}); assert(s~=nil and e==nil)`,
		"ordered array":  `local s,e=json.encode({3,2,1}); assert(s=="[3,2,1]" and e==nil)`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := L.DoString(source); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestJSONConversionChecksCancellation(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	L.SetContext(ctx)
	budget := &jsonBudget{L: L, active: make(map[*lua.LTable]bool)}
	if _, err := budget.luaToGo(L.NewTable(), 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("encode cancellation = %v", err)
	}
	if _, err := budget.goToLua(map[string]any{"key": 1.0}, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("decode cancellation = %v", err)
	}
}

func TestStandaloneHTTPIsStrictAndRedactsErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	L := NewSandboxedState(Bindings{})
	defer L.Close()
	L.SetGlobal("URL", lua.LString(strings.Replace(srv.URL, "http://", "http://username:password@", 1)+"/?token=secret"))
	if err := L.DoString(`local r,e=http.get(URL); assert(r==nil and e~=nil); assert(not string.find(e,"secret")); assert(not string.find(e,"password"))`); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatal("standalone plugin reached a loopback destination")
	}
}
