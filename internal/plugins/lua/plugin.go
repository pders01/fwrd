package lua

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/pders01/fwrd/internal/plugins"
	lua "github.com/yuin/gopher-lua"
)

// Plugin adapts a Lua script to the plugins.Plugin interface. Each
// plugin owns a single *lua.LState; concurrent EnhanceFeed calls
// serialise through a cancellable gate because gopher-lua states are not
// goroutine-safe.
type Plugin struct {
	name     string
	priority int
	path     string

	gate    chan struct{}
	timeout time.Duration
	state   *lua.LState
	table   *lua.LTable
}

// Compile-time check that Plugin satisfies plugins.Plugin.
var _ plugins.Plugin = (*Plugin)(nil)

// Name returns the plugin name declared in the script's returned table.
func (p *Plugin) Name() string { return p.name }

// Priority returns the plugin priority declared in the script's returned table.
func (p *Plugin) Priority() int { return p.priority }

// Path returns the absolute filesystem path of the .lua file backing
// this plugin. CLI inspection commands use it to show users where a
// loaded plugin came from.
func (p *Plugin) Path() string { return p.path }

// CanHandle invokes the plugin's can_handle(url) and returns the boolean
// result. Errors and non-boolean returns are treated as false so a buggy
// plugin cannot poison URL routing for the rest of the registry.
func (p *Plugin) CanHandle(url string) bool {
	return p.CanHandleContext(context.Background(), url)
}

// CanHandleContext bounds both admission and Lua routing execution.
func (p *Plugin) CanHandleContext(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, min(p.timeout, bootstrapTimeout))
	defer cancel()
	if err := p.acquire(ctx); err != nil {
		return false
	}
	defer p.release()
	if p.state == nil {
		return false
	}
	p.state.SetContext(ctx)
	defer p.state.RemoveContext()
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
func (p *Plugin) EnhanceFeed(ctx context.Context, url string, _ *http.Client) (*plugins.FeedInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	if err := p.acquire(ctx); err != nil {
		return nil, err
	}
	defer p.release()

	if p.state == nil {
		return nil, errors.New("plugin closed")
	}
	p.state.SetContext(ctx)
	defer p.state.RemoveContext()

	fn := p.state.GetField(p.table, "enhance")
	if _, ok := fn.(*lua.LFunction); !ok {
		return nil, errors.New("plugin missing enhance() function")
	}
	p.state.Push(fn)
	p.state.Push(lua.LString(url))
	if err := p.state.PCall(1, 1, nil); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("plugin enhance failed: %w", err)
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
		Metadata:    map[string]string{},
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
	// Stamp the plugin name last so user-supplied metadata cannot
	// overwrite the host's authoritative attribution key.
	info.Metadata["plugin"] = p.name
	if info.FeedURL == "" {
		info.FeedURL = url
	}
	return info, nil
}

// Close releases the underlying Lua state. After Close the plugin must
// not be used again.
func (p *Plugin) Close() {
	p.gate <- struct{}{}
	defer p.release()
	if p.state != nil {
		p.state.Close()
		p.state = nil
		p.table = nil
	}
}

// DefaultExecutionTimeout preserves the normal HTTP enhancement budget.
// Loading and pure URL routing receive a shorter independent ceiling.
const (
	DefaultExecutionTimeout = 30 * time.Second
	bootstrapTimeout        = 2 * time.Second
)

func executionTimeout(b Bindings) time.Duration {
	if b.ExecutionTimeout > 0 {
		return b.ExecutionTimeout
	}
	return DefaultExecutionTimeout
}

func (p *Plugin) acquire(ctx context.Context) error {
	select {
	case p.gate <- struct{}{}:
		if err := ctx.Err(); err != nil {
			p.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Plugin) release() { <-p.gate }

func tableString(t *lua.LTable, key string) string {
	v, ok := t.RawGetString(key).(lua.LString)
	if !ok {
		return ""
	}
	return string(v)
}
