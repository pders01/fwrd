package lua

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	lua "github.com/yuin/gopher-lua"
)

// MaxScriptSize caps the byte size of a single .lua plugin file.
// Anything larger is rejected before the Lua VM sees it. 256 KiB is
// generous for a feed-URL handler and small enough that infinite loops
// have nowhere obvious to hide.
const MaxScriptSize int64 = 256 * 1024

// LoadResult is one outcome of scanning a plugin directory: either a
// plugin loaded successfully, or a plugin file failed to load with the
// associated error. The loader returns one LoadResult per .lua file
// found.
type LoadResult struct {
	Path   string
	Plugin *LuaPlugin
	Err    error
}

// LoadDir scans dir for *.lua files and loads each through the
// sandboxed runtime. Files that exceed MaxScriptSize, fail to parse,
// or return an invalid plugin table are reported as LoadResult.Err
// and skipped — one bad file must not block plugin loading for the
// rest of the directory.
//
// Returns nil with no error when dir does not exist; this lets the
// caller treat "no plugin directory" as the empty case without a
// pre-flight stat.
func LoadDir(dir string, b Bindings) ([]LoadResult, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading plugin dir: %w", err)
	}

	results := make([]LoadResult, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".lua" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		plugin, err := LoadFile(path, b)
		results = append(results, LoadResult{Path: path, Plugin: plugin, Err: err})
	}

	sort.Slice(results, func(i, j int) bool { return results[i].Path < results[j].Path })
	return results, nil
}

// LoadFile loads a single Lua plugin file. The returned LuaPlugin owns
// a sandboxed *lua.LState that lives until LuaPlugin.Close is called.
func LoadFile(path string, b Bindings) (*LuaPlugin, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening plugin: %w", err)
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat plugin: %w", err)
	}
	if stat.Size() > MaxScriptSize {
		return nil, fmt.Errorf("plugin exceeds %d bytes", MaxScriptSize)
	}

	source, err := io.ReadAll(io.LimitReader(f, MaxScriptSize+1))
	if err != nil {
		return nil, fmt.Errorf("reading plugin: %w", err)
	}
	if int64(len(source)) > MaxScriptSize {
		return nil, fmt.Errorf("plugin exceeds %d bytes", MaxScriptSize)
	}

	L := NewSandboxedState(b)
	chunk, err := L.LoadString(string(source))
	if err != nil {
		L.Close()
		return nil, fmt.Errorf("parsing plugin: %w", err)
	}
	L.Push(chunk)
	if err := L.PCall(0, 1, nil); err != nil {
		L.Close()
		return nil, fmt.Errorf("running plugin: %w", err)
	}

	tbl, ok := L.Get(-1).(*lua.LTable)
	if !ok {
		L.Close()
		return nil, errors.New("plugin must return a table")
	}
	L.Pop(1)

	name := tableString(tbl, "name")
	if name == "" {
		L.Close()
		return nil, errors.New("plugin table missing name")
	}
	if _, ok := tbl.RawGetString("can_handle").(*lua.LFunction); !ok {
		L.Close()
		return nil, errors.New("plugin table missing can_handle function")
	}
	if _, ok := tbl.RawGetString("enhance").(*lua.LFunction); !ok {
		L.Close()
		return nil, errors.New("plugin table missing enhance function")
	}

	priority := 0
	if n, ok := tbl.RawGetString("priority").(lua.LNumber); ok {
		priority = int(n)
	}

	return &LuaPlugin{
		name:     name,
		priority: priority,
		path:     path,
		state:    L,
		table:    tbl,
	}, nil
}
