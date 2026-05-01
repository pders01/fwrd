package lua

import (
	lua "github.com/yuin/gopher-lua"
)

// registerLog exposes log.info(msg) / log.warn(msg). When logger is nil
// the calls are no-ops, which keeps tests trivial.
func registerLog(L *lua.LState, logger Logger) {
	tbl := L.NewTable()
	L.SetField(tbl, "info", L.NewFunction(func(L *lua.LState) int {
		if logger != nil {
			logger.Infof("[lua-plugin] %s", L.CheckString(1))
		}
		return 0
	}))
	L.SetField(tbl, "warn", L.NewFunction(func(L *lua.LState) int {
		if logger != nil {
			logger.Warnf("[lua-plugin] %s", L.CheckString(1))
		}
		return 0
	}))
	L.SetGlobal("log", tbl)
}
