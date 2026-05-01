// Package lua implements the scriptable plugin runtime that lets users
// drop .lua files into the plugin directory to enhance feed URLs without
// recompiling fwrd.
package lua

import (
	"net/http"

	lua "github.com/yuin/gopher-lua"
)

// Bindings carries the host capabilities exposed to a Lua plugin. It is
// supplied by the loader and reused for every host binding registered
// onto a sandboxed LState.
type Bindings struct {
	HTTPClient *http.Client
	Logger     Logger
}

// Logger is the minimal logging surface plugins call through log.info /
// log.warn, and that the loader uses to report skipped plugins. The
// printf-style signature mirrors fwrd's debuglog package so the host
// can wire in a one-line adapter.
type Logger interface {
	Infof(format string, args ...any)
	Warnf(format string, args ...any)
}

// safeLibs lists the gopher-lua standard libraries that are safe to open
// in a plugin sandbox.
var safeLibs = []struct {
	name string
	open lua.LGFunction
}{
	{lua.BaseLibName, lua.OpenBase},
	{lua.TabLibName, lua.OpenTable},
	{lua.StringLibName, lua.OpenString},
	{lua.MathLibName, lua.OpenMath},
}

// bannedGlobals enumerates base-library functions that allow code
// execution, filesystem access, environment manipulation, or sandbox
// integrity violations. They are removed after OpenBase runs.
//
// `print` is removed because gopher-lua wires it to os.Stdout and a
// plugin author calling print() inside the TUI would corrupt the
// Bubble Tea alt-screen. Plugins should call log.info / log.warn.
//
// `setmetatable` and `getmetatable` are removed because they let a
// plugin replace the metatable of a primitive type (string, number)
// shared by the entire LState — even though each plugin owns its own
// state, that one plugin can break its own stdlib in opaque ways.
// `newproxy` is gopher-lua's userdata-with-metatable factory which
// shares the same risk.
var bannedGlobals = []string{
	"dofile",
	"loadfile",
	"loadstring",
	"load",
	"require",
	"module",
	"getfenv",
	"setfenv",
	"rawget",
	"rawset",
	"rawequal",
	"collectgarbage",
	"print",
	"setmetatable",
	"getmetatable",
	"newproxy",
}

// NewSandboxedState returns a fresh *lua.LState with whitelisted stdlib
// loaded, dangerous globals removed, and the host bindings registered.
// The caller owns the state and must Close it.
func NewSandboxedState(b Bindings) *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	for _, lib := range safeLibs {
		L.Push(L.NewFunction(lib.open))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}
	for _, name := range bannedGlobals {
		L.SetGlobal(name, lua.LNil)
	}
	registerHTTP(L, b.HTTPClient)
	registerJSON(L)
	registerRegex(L)
	registerLog(L, b.Logger)
	return L
}
