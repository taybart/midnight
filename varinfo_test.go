package lua

import (
	"strings"
	"testing"
)

// Errors name the variable a bad value came from.
func TestVarInfo(t *testing.T) {
	cases := []struct{ src, want string }{
		{`local f; f()`, `attempt to call a nil value (local 'f')`},
		{`nope()`, `attempt to call a nil value (global 'nope')`},
		{`local t = {}; t.x()`, `attempt to call a nil value (field 'x')`},
		{`local t = {}; t:m()`, `attempt to call a nil value (method 'm')`},
		{`local t = {}; t.a.b.c()`, `attempt to index a nil value (field 'a') with key 'b'`},
		{`local u; (function() u() end)()`, `attempt to call a nil value (upvalue 'u')`},
		{`local p = {}; return p.repo.name`, `attempt to index a nil value (field 'repo') with key 'name'`},
		{`local x; return x.y`, `attempt to index a nil value (local 'x') with key 'y'`},
		{`local x = 1; x.y = 2`, `attempt to index a number value (local 'x') with key 'y'`},
		{`local t = {}; t.a.b = 1`, `attempt to index a nil value (field 'a') with key 'b'`},
		{`local x = 'str'; x()`, `attempt to call a string value (local 'x')`},
		{`local t = {}; return t[1]()`, `attempt to call a nil value`},
		{`local function g() return nil end; g()()`, `attempt to call a nil value`},
		{`local x; local function f() return x end; return f().y`, `attempt to index a nil value with key 'y'`},
		{`local a = nil; local b = a; b()`, `attempt to call a nil value (local 'b')`},
		{`return (cond and nil or nil)()`, `attempt to call a nil value`},
		{`local a = 1; do local c; c() end`, `attempt to call a nil value (local 'c')`},
		{`local a = 1; do local c = 2 end; local d; d()`, `attempt to call a nil value (local 'd')`},
		{`for i = 1, 2 do local v; v() end`, `attempt to call a nil value (local 'v')`},
		{`local function f(arg) arg.x = 1 end; f()`, `attempt to index a nil value (local 'arg') with key 'x'`},
		{`local t = setmetatable({}, { __index = function() return nil end }); t.x()`, `attempt to call a nil value (field 'x')`},
		{`do local c; c.y = 1 end`, `attempt to index a nil value (local 'c') with key 'y'`},
		{`local a; do local b = 1 end; local c = function() end; a.x = 1`, `attempt to index a nil value (local 'a') with key 'x'`},
		{`local s = 'abc'; s.x.y = 1`, `attempt to index a nil value (field 'x') with key 'y'`},
	}
	for _, c := range cases {
		L := NewState()
		err := L.DoString(c.src)
		L.Close()
		if err == nil {
			t.Errorf("%s: no error", c.src)
			continue
		}
		msg := err.Error()
		if i := strings.Index(msg, "\n"); i >= 0 {
			msg = msg[:i]
		}
		if !strings.HasSuffix(msg, c.want) {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, msg, c.want)
		}
	}
}

// Midnight's temporaries aren't named: errors describe what was put in them.
func TestVarInfoMidnight(t *testing.T) {
	cases := []struct{ src, want string }{
		{`local { elements } = {}; elements('/x.js', {})`, `attempt to call a nil value (local 'elements')`},
		{`local x; local { a } = x`, `attempt to index a nil value (local 'x') with key 'a'`},
		{`local props = {}; local Page = (p) -> p.repo.name; Page(props)`, `attempt to index a nil value (field 'repo') with key 'name'`},
		{`local Page = ({ repo }) -> repo.name; Page({})`, `attempt to index a nil value (local 'repo') with key 'name'`},
	}
	for _, c := range cases {
		L := NewState()
		err := L.DoMidnightString(c.src)
		L.Close()
		if err == nil {
			t.Errorf("%s: no error", c.src)
			continue
		}
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		if !strings.HasSuffix(msg, c.want) {
			t.Errorf("%s\n  got  %s\n  want %s", c.src, msg, c.want)
		}
	}
}
