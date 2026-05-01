package lua

import (
	"encoding/json"

	lua "github.com/yuin/gopher-lua"
)

// registerJSON exposes json.parse(s) and json.encode(v).
//
// Parse returns the decoded value plus an error string (nil on success).
// Encode returns the encoded string plus an error string (nil on
// success). Errors are returned rather than raised so plugins can pcall
// without losing structure.
func registerJSON(L *lua.LState) {
	tbl := L.NewTable()
	L.SetField(tbl, "parse", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(goToLua(L, v))
		L.Push(lua.LNil)
		return 2
	}))
	L.SetField(tbl, "encode", L.NewFunction(func(L *lua.LState) int {
		v := luaToGo(L.CheckAny(1))
		buf, err := json.Marshal(v)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LString(string(buf)))
		L.Push(lua.LNil)
		return 2
	}))
	L.SetGlobal("json", tbl)
}

// goToLua converts a JSON-decoded Go value into the corresponding Lua
// value. Maps become tables keyed by string; slices become 1-indexed
// arrays; numbers become LNumber; bools become LBool; nil becomes LNil.
func goToLua(L *lua.LState, v any) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case string:
		return lua.LString(x)
	case []any:
		t := L.NewTable()
		for i, item := range x {
			t.RawSetInt(i+1, goToLua(L, item))
		}
		return t
	case map[string]any:
		t := L.NewTable()
		for k, item := range x {
			t.RawSetString(k, goToLua(L, item))
		}
		return t
	default:
		return lua.LNil
	}
}

// luaToGo converts a Lua value to a JSON-marshalable Go value. A table
// with consecutive integer keys starting at 1 becomes a slice; any
// other table becomes a map. This mirrors how plugin authors think of
// Lua tables (array vs object) and matches how json.encode behaves in
// other languages.
func luaToGo(v lua.LValue) any {
	switch x := v.(type) {
	case lua.LBool:
		return bool(x)
	case lua.LNumber:
		return float64(x)
	case lua.LString:
		return string(x)
	case *lua.LTable:
		if isArrayTable(x) {
			arr := make([]any, 0, x.Len())
			x.ForEach(func(_, val lua.LValue) {
				arr = append(arr, luaToGo(val))
			})
			return arr
		}
		m := make(map[string]any)
		x.ForEach(func(k, val lua.LValue) {
			if ks, ok := k.(lua.LString); ok {
				m[string(ks)] = luaToGo(val)
			}
		})
		return m
	default:
		return nil
	}
}

func isArrayTable(t *lua.LTable) bool {
	n := t.Len()
	if n == 0 {
		return false
	}
	for i := 1; i <= n; i++ {
		if t.RawGetInt(i) == lua.LNil {
			return false
		}
	}
	return true
}
