package lua

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/pders01/fwrd/internal/plugins"
	lua "github.com/yuin/gopher-lua"
)

// LuaPlugin adapts a Lua script to the plugins.Plugin interface. Each
// plugin owns a single *lua.LState; concurrent EnhanceFeed calls
// serialise through a mutex because gopher-lua states are not
// goroutine-safe.
type LuaPlugin struct {
	name     string
	priority int
	path     string

	mu    sync.Mutex
	state *lua.LState
	table *lua.LTable
}

// Compile-time check that LuaPlugin satisfies plugins.Plugin.
var _ plugins.Plugin = (*LuaPlugin)(nil)

// Name returns the plugin name declared in the script's returned table.
func (p *LuaPlugin) Name() string { return p.name }

// Priority returns the plugin priority declared in the script's returned table.
func (p *LuaPlugin) Priority() int { return p.priority }

// CanHandle invokes the plugin's can_handle(url) and returns the boolean
// result. Errors and non-boolean returns are treated as false so a buggy
// plugin cannot poison URL routing for the rest of the registry.
func (p *LuaPlugin) CanHandle(url string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn := p.state.GetField(p.table, "can_handle")
	if _, ok := fn.(*lua.LFunction); !ok {
		return false
	}
	p.state.Push(fn)
	p.state.Push(lua.LString(url))
	if err := p.state.PCall(1, 1, nil); err != nil {
		return false
	}
	defer p.state.Pop(1)
	b, ok := p.state.Get(-1).(lua.LBool)
	return ok && bool(b)
}

// EnhanceFeed invokes the plugin's enhance(url) under the supplied
// context. The HTTP client argument is unused: the host-side Lua HTTP
// binding is wired at sandbox construction time.
func (p *LuaPlugin) EnhanceFeed(ctx context.Context, url string, _ *http.Client) (*plugins.FeedInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.state.SetContext(ctx)
	defer p.state.RemoveContext()

	fn := p.state.GetField(p.table, "enhance")
	if _, ok := fn.(*lua.LFunction); !ok {
		return nil, errors.New("plugin missing enhance() function")
	}
	p.state.Push(fn)
	p.state.Push(lua.LString(url))
	if err := p.state.PCall(1, 1, nil); err != nil {
		return nil, fmt.Errorf("enhance(%q): %w", url, err)
	}
	defer p.state.Pop(1)

	ret, ok := p.state.Get(-1).(*lua.LTable)
	if !ok {
		return nil, errors.New("enhance() must return a table")
	}

	info := &plugins.FeedInfo{
		OriginalURL: url,
		FeedURL:     tableString(ret, "feed_url"),
		Title:       tableString(ret, "title"),
		Description: tableString(ret, "description"),
		Metadata:    map[string]string{"plugin": p.name},
	}
	if md, mdOK := ret.RawGetString("metadata").(*lua.LTable); mdOK {
		md.ForEach(func(k, v lua.LValue) {
			ks, kok := k.(lua.LString)
			vs, vok := v.(lua.LString)
			if kok && vok {
				info.Metadata[string(ks)] = string(vs)
			}
		})
	}
	if info.FeedURL == "" {
		info.FeedURL = url
	}
	return info, nil
}

// Close releases the underlying Lua state. After Close the plugin must
// not be used again.
func (p *LuaPlugin) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state != nil {
		p.state.Close()
		p.state = nil
		p.table = nil
	}
}

func tableString(t *lua.LTable, key string) string {
	v, ok := t.RawGetString(key).(lua.LString)
	if !ok {
		return ""
	}
	return string(v)
}
