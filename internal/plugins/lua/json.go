package lua

import (
	"encoding/json"
	"errors"

	lua "github.com/yuin/gopher-lua"
)

const (
	jsonMaxDepth = 64
	jsonMaxNodes = 100000
	jsonMaxBytes = 4 * 1024 * 1024
)

type jsonBudget struct {
	L      *lua.LState
	nodes  int
	bytes  int
	active map[*lua.LTable]bool
}

func (b *jsonBudget) visit(depth, size int) error {
	if ctx := b.L.Context(); ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	b.nodes++
	b.bytes += size
	if depth > jsonMaxDepth || b.nodes > jsonMaxNodes || b.bytes > jsonMaxBytes {
		return errors.New("JSON conversion budget exceeded")
	}
	return nil
}

// registerJSON exposes bounded, cancellation-aware json.parse and json.encode.
// Conversion errors are returned as (nil, error), never raised into the VM.
func registerJSON(L *lua.LState) {
	tbl := L.NewTable()
	L.SetField(tbl, "parse", L.NewFunction(func(L *lua.LState) int {
		s := L.CheckString(1)
		b := &jsonBudget{L: L}
		if err := b.visit(0, len(s)); err != nil {
			return pushJSONError(L, err)
		}
		var v any
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return pushJSONError(L, err)
		}
		value, err := b.goToLua(v, 0)
		if err != nil {
			return pushJSONError(L, err)
		}
		L.Push(value)
		L.Push(lua.LNil)
		return 2
	}))
	L.SetField(tbl, "encode", L.NewFunction(func(L *lua.LState) int {
		b := &jsonBudget{L: L, active: make(map[*lua.LTable]bool)}
		v, err := b.luaToGo(L.CheckAny(1), 0)
		if err != nil {
			return pushJSONError(L, err)
		}
		buf, err := json.Marshal(v)
		if err != nil {
			return pushJSONError(L, err)
		}
		if len(buf) > jsonMaxBytes {
			return pushJSONError(L, errors.New("JSON output budget exceeded"))
		}
		if err := b.visit(0, 0); err != nil {
			return pushJSONError(L, err)
		}
		L.Push(lua.LString(string(buf)))
		L.Push(lua.LNil)
		return 2
	}))
	L.SetGlobal("json", tbl)
}

func pushJSONError(L *lua.LState, err error) int {
	L.Push(lua.LNil)
	L.Push(lua.LString(err.Error()))
	return 2
}

func (b *jsonBudget) goToLua(v any, depth int) (lua.LValue, error) {
	if err := b.visit(depth, 0); err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case nil:
		return lua.LNil, nil
	case bool:
		return lua.LBool(x), nil
	case float64:
		return lua.LNumber(x), nil
	case string:
		return lua.LString(x), nil // Input bytes were already charged at parse.
	case []any:
		t := b.L.NewTable()
		for i, item := range x {
			value, err := b.goToLua(item, depth+1)
			if err != nil {
				return nil, err
			}
			t.RawSetInt(i+1, value)
		}
		return t, nil
	case map[string]any:
		t := b.L.NewTable()
		for k, item := range x {
			value, err := b.goToLua(item, depth+1)
			if err != nil {
				return nil, err
			}
			t.RawSetString(k, value)
		}
		return t, nil
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

func (b *jsonBudget) luaToGo(v lua.LValue, depth int) (any, error) {
	if err := b.visit(depth, 0); err != nil {
		return nil, err
	}
	switch x := v.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(x), nil
	case lua.LNumber:
		return float64(x), nil
	case lua.LString:
		if err := b.visit(depth, len(x)); err != nil {
			return nil, err
		}
		return string(x), nil
	case *lua.LTable:
		if b.active[x] {
			return nil, errors.New("cyclic JSON table")
		}
		b.active[x] = true
		defer delete(b.active, x)
		return b.tableToGo(x, depth)
	default:
		return nil, errors.New("unsupported JSON value")
	}
}

func (b *jsonBudget) tableToGo(t *lua.LTable, depth int) (any, error) {
	values := make(map[lua.LValue]any)
	array := true
	for key, value := t.Next(lua.LNil); key != lua.LNil; key, value = t.Next(key) {
		if err := b.visit(depth+1, len(key.String())); err != nil {
			return nil, err
		}
		converted, err := b.luaToGo(value, depth+1)
		if err != nil {
			return nil, err
		}
		values[key] = converted
		if n, ok := key.(lua.LNumber); !ok || n < 1 || n != lua.LNumber(int(n)) {
			array = false
		}
	}
	if array && len(values) > 0 {
		result := make([]any, len(values))
		for i := range result {
			value, ok := values[lua.LNumber(i+1)]
			if !ok {
				return nil, errors.New("sparse JSON array")
			}
			result[i] = value
		}
		return result, nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		k, ok := key.(lua.LString)
		if !ok {
			return nil, errors.New("JSON object keys must be strings")
		}
		result[string(k)] = value
	}
	return result, nil
}
