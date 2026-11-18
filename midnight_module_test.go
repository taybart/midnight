package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// Module names are hoisted (visible before their declaration), and `module.x`
// always refers to the module's `x`. Each source returns the module, and `want`
// is the result of `m.check()`.
var midnightModuleCases = []struct {
	source string
	want   string
}{
	// forward references to functions and variables
	{`module function check() { return later() } module function later() { return 'ok' }`, "ok"},
	{`module function check() { return x + 1 } module x = 1`, "2"},
	{`module function check() { return f() } module f = () -> 3`, "3"},
	{`module function check() { return A.new():v() } module class A { v() { return 4 } }`, "4"},
	{`module function check() { return a + b } module { a, b } = { a = 1, b = 2 }`, "3"},
	{`module function check() { return a + b } module [a, b] = { 1, 2 }`, "3"},
	// from inside closures
	{`module function check() { local g = () -> h(); return g() } module function h() { return 5 }`, "5"},
	// locals still shadow hoisted names
	{`module function check() { local later = () -> 'local'; return later() } module function later() { return 'module' }`, "local"},
	{`local function f() { return 'local' } module function check() { return f() } module function f() { return 'module' }`, "local"},
	// module.x gets past a shadowing local
	{`module opts = 1 module function check(opts = 10) { module.opts = module.opts + opts; return module.opts }`, "11"},
	{`module function check() { local f = 1; return module.f() + f } module function f() { return 1 }`, "2"},
	// module.x works w/o other module declarations
	{`module.v = 6 module.check = () -> module.v`, "6"},
	{`local function check() { return module.v } module.v = 7 module.check = check`, "7"},
}

func midnightModuleCheck(t *testing.T, L *LState, label string) string {
	t.Helper()
	L.SetGlobal("m", L.Get(-1))
	if err := L.DoString(`return m.check()`); err != nil {
		t.Errorf("%s\n%v", label, err)
		return ""
	}
	return L.Get(-1).String()
}

func TestMidnightModule(t *testing.T) {
	for _, c := range midnightModuleCases {
		L := NewState()
		if err := L.DoMidnightString(c.source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if got := midnightModuleCheck(t, L, c.source); got != c.want {
			t.Errorf("%s\n  got %s, want %s", c.source, got, c.want)
		}
		L.Close()
	}
}

func TestLuagenMidnightModule(t *testing.T) {
	for _, c := range midnightModuleCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
		} else if got := midnightModuleCheck(t, L, generated); got != c.want {
			t.Errorf("%s\n%s\n  got %s, want %s", c.source, generated, got, c.want)
		}
		L.Close()
	}
}

func TestMidnightModuleSyntaxErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{`local x = module`, "unexpected keyword 'module'"},
		{`module.x = 1; return 1`, "cannot use 'return' w/ 'module' declarations"},
		// errors from the hoisting pass are the same as without it
		{"module x = 1\nlocal = 2", "line:2"},
	}

	for _, c := range cases {
		L := NewState()
		_, err := L.LoadMidnightString(c.source)
		L.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got %v, want error containing %q", c.source, err, c.want)
		}
	}
}
