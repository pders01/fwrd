package lua

import (
	"context"
	"errors"
	"io"
	"net/http"

	lua "github.com/yuin/gopher-lua"
)

// httpBodyCap caps the body size returned by http.get to prevent a
// malicious upstream from driving a plugin OOM. Mirrors
// feed.maxFeedBodySize.
const httpBodyCap int64 = 50 * 1024 * 1024 // 50 MiB

// registerHTTP exposes http.get(url[, opts]) which performs a blocking
// HTTP GET via the host-provided client, returning a result table on
// success and (nil, errString) on failure.
//
// Result table fields:
//   - status : integer HTTP status code
//   - body   : response body as string (capped to httpBodyCap)
//   - headers: table of {[name]=value}; only first value per header
//
// Opts table (optional) fields:
//   - headers: table of request headers to add
func registerHTTP(L *lua.LState, client *http.Client) {
	if client == nil {
		client = http.DefaultClient
	}
	tbl := L.NewTable()
	L.SetField(tbl, "get", L.NewFunction(func(L *lua.LState) int {
		url := L.CheckString(1)
		opts := L.OptTable(2, nil)

		ctx := L.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
		if err != nil {
			return pushHTTPErr(L, err)
		}
		applyOptHeaders(req, opts)

		resp, err := client.Do(req)
		if err != nil {
			return pushHTTPErr(L, err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, httpBodyCap))
		if err != nil {
			return pushHTTPErr(L, err)
		}

		result := L.NewTable()
		L.SetField(result, "status", lua.LNumber(resp.StatusCode))
		L.SetField(result, "body", lua.LString(string(body)))

		headers := L.NewTable()
		for k, v := range resp.Header {
			if len(v) > 0 {
				L.SetField(headers, k, lua.LString(v[0]))
			}
		}
		L.SetField(result, "headers", headers)

		L.Push(result)
		L.Push(lua.LNil)
		return 2
	}))
	L.SetGlobal("http", tbl)
}

func applyOptHeaders(req *http.Request, opts *lua.LTable) {
	if opts == nil {
		return
	}
	headers, ok := opts.RawGetString("headers").(*lua.LTable)
	if !ok {
		return
	}
	headers.ForEach(func(k, v lua.LValue) {
		ks, kok := k.(lua.LString)
		vs, vok := v.(lua.LString)
		if kok && vok {
			req.Header.Set(string(ks), string(vs))
		}
	})
}

func pushHTTPErr(L *lua.LState, err error) int {
	if err == nil {
		err = errors.New("unknown http error")
	}
	L.Push(lua.LNil)
	L.Push(lua.LString(err.Error()))
	return 2
}
