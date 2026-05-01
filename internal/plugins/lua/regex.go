package lua

import (
	"regexp"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

// reCache memoises compiled regex patterns across all plugins. Plugin
// hot paths (can_handle, enhance) call regex.match with the same
// constant pattern repeatedly, and Go's regexp package does no internal
// caching — so an unguarded binding pays a full compile per call.
var reCache sync.Map // map[string]*regexp.Regexp

func cachedCompile(pattern string) (*regexp.Regexp, error) {
	if v, ok := reCache.Load(pattern); ok {
		return v.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	actual, _ := reCache.LoadOrStore(pattern, re)
	return actual.(*regexp.Regexp), nil
}

// registerRegex exposes regex.match(pattern, subject) which returns the
// first capture group when the pattern matches, the whole match when
// the pattern has no capture groups, or nil when there is no match.
//
// Go's regexp/syntax (RE2) is used, not Lua patterns. Plugin authors
// targeting fwrd should write Go-flavoured regexes.
func registerRegex(L *lua.LState) {
	tbl := L.NewTable()
	L.SetField(tbl, "match", L.NewFunction(func(L *lua.LState) int {
		pattern := L.CheckString(1)
		subject := L.CheckString(2)
		re, err := cachedCompile(pattern)
		if err != nil {
			L.Push(lua.LNil)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		matches := re.FindStringSubmatch(subject)
		if matches == nil {
			L.Push(lua.LNil)
			return 1
		}
		if len(matches) > 1 {
			L.Push(lua.LString(matches[1]))
		} else {
			L.Push(lua.LString(matches[0]))
		}
		return 1
	}))
	L.SetGlobal("regex", tbl)
}
