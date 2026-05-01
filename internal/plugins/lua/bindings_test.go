package lua

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	gluapkg "github.com/yuin/gopher-lua"
)

var fmtSprintf = fmt.Sprintf

func TestRegexMatch(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	if err := L.DoString(`
local got = regex.match("/r/([a-z]+)", "https://reddit.com/r/golang/comments")
assert(got == "golang", "expected 'golang' got " .. tostring(got))
assert(regex.match("nope", "abc") == nil)`); err != nil {
		t.Fatal(err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	L := NewSandboxedState(Bindings{})
	defer L.Close()

	if err := L.DoString(`
local v, err = json.parse('{"a":1,"b":[1,2,3]}')
assert(err == nil)
assert(v.a == 1)
assert(v.b[2] == 2)

local s, err2 = json.encode({a=1, b={1,2,3}})
assert(err2 == nil)
assert(type(s) == "string")`); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPGetSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Test"); got != "yes" {
			t.Errorf("header X-Test missing, got %q", got)
		}
		w.Header().Set("X-Reply", "hello")
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	L := NewSandboxedState(Bindings{HTTPClient: srv.Client()})
	defer L.Close()
	L.SetGlobal("URL", gluapkg.LString(srv.URL))

	if err := L.DoString(`
local res, err = http.get(URL, {headers={["X-Test"]="yes"}})
assert(err == nil, tostring(err))
assert(res.status == 200)
assert(res.body == "ok")
assert(res.headers["X-Reply"] == "hello")`); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPGetErrorReturnsNilBody(t *testing.T) {
	L := NewSandboxedState(Bindings{HTTPClient: http.DefaultClient})
	defer L.Close()

	if err := L.DoString(`
local res, err = http.get("http://127.0.0.1:0/nope")
assert(res == nil)
assert(type(err) == "string")`); err != nil {
		t.Fatal(err)
	}
}

type captureLogger struct {
	mu       sync.Mutex
	infos    []string
	warnings []string
}

func (c *captureLogger) Infof(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.infos = append(c.infos, fmtSprintf(format, args...))
}
func (c *captureLogger) Warnf(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.warnings = append(c.warnings, fmtSprintf(format, args...))
}

func TestLogBindings(t *testing.T) {
	cap := &captureLogger{}
	L := NewSandboxedState(Bindings{Logger: cap})
	defer L.Close()

	if err := L.DoString(`log.info("hello") log.warn("oops")`); err != nil {
		t.Fatal(err)
	}
	if len(cap.infos) != 1 || !strings.Contains(cap.infos[0], "hello") {
		t.Errorf("info: %v", cap.infos)
	}
	if len(cap.warnings) != 1 || !strings.Contains(cap.warnings[0], "oops") {
		t.Errorf("warn: %v", cap.warnings)
	}
}
