package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// const declares a local (or module name) that can't be assigned again. It's
// checked when compiling: the code is the same as w/ local / module.
var midnightConstCases = []struct {
	source string
	want   string
}{
	{`const x = 1; return x`, "1"},
	{`const a, b = 1, 2; return a + b`, "3"},
	{`const { a, b: c = 3 } = { a = 1 }; return a + c`, "4"},
	{`const [a, b] = { 1, 2 }; return a + b`, "3"},
	{`const f = (n) -> n * 2; return f(2)`, "4"},
	// a table literal is frozen (and the literals in it); other values aren't
	{`const t = { a = { 1 } }; return tostring(table.isfrozen(t)) .. tostring(table.isfrozen(t.a))`, "truetrue"},
	{`const t = { 1, 2, n = 3 }; local s = 0; for _, v in ipairs(t) { s += v }; return s + t.n + #t`, "8"},
	{`local function make() { return {} }; const t = make(); t.x = 5; return t.x`, "5"},
	{`const t = setmetatable({}, {}); function t.f() { return 6 }; return t.f()`, "6"},
	{`local inner = {}; const t = { inner = inner }; inner.x = 5; return t.inner.x`, "5"},
	{`const { a } = { a = {} }; a.x = 5; return a.x`, "5"},
	{`local t = { x = 1 }; t.x = 5; return t.x`, "5"},
	// a new local w/ the same name is a new variable
	{`const x = 1; do { local x = 2; x = 7; return x }`, "7"},
	{`const x = 1; local function f(x) { x = 8; return x }; return f(1)`, "8"},
	{`const x = 1; for x = 1, 2 { x = 9; return x }`, "9"},
	{`const x = 1; local x = 2; x = 10; return x`, "10"},
	{`const e = 1; local v = error('x') catch e { e = 11; return e }; return v`, "11"},
	// const is still a name, when it isn't declaring something
	{`local const = 1; const = 12; return const`, "12"},
	{`local const = {}; const[1] = 13; return const[1]`, "13"},
	{`local const = (v) -> v; return const(14)`, "14"},
}

func TestMidnightConst(t *testing.T) {
	for _, c := range midnightConstCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Errorf("%s\n%v", c.source, err)
			continue
		}
		generated := luagen.Generate(chunk)
		for name, run := range map[string]func(L *LState) error{
			"native": func(L *LState) error { return L.DoMidnightString(c.source) },
			"luagen": func(L *LState) error { return L.DoString(generated) },
		} {
			L := NewState()
			if err := run(L); err != nil {
				t.Errorf("%s (%s)\n%v", c.source, name, err)
			} else if got := L.Get(-1).String(); got != c.want {
				t.Errorf("%s (%s)\n  got  %s\n  want %s", c.source, name, got, c.want)
			}
			L.Close()
		}
	}
}

// Writing to a const table literal is an error (from midnight's runtime, or
// from luagen's polyfill on other runtimes).
func TestMidnightConstFrozen(t *testing.T) {
	writes := []string{
		`const t = { a = 1 }; t.a = 2`,
		`const t = { a = 1 }; t.b = 2`,
		`const t = { a = { b = 1 } }; t.a.b = 2`,
		`module const t = { a = 1 }; t.a = 2`,
		`const t = { a = 1 }; setmetatable(t, {})`,
		`const t = {}; function t.f() {}`,
	}
	for _, source := range writes {
		chunk, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", source, err)
		}
		generated := luagen.Generate(chunk)
		for name, run := range map[string]func(L *LState) error{
			"native": func(L *LState) error { return L.DoMidnightString(source) },
			"luagen": func(L *LState) error { return L.DoString(generated) },
			// (w/o the runtime's table.freeze, so the polyfill defines it)
			"polyfill": func(L *LState) error {
				return L.DoString("table.freeze, table.isfrozen = nil, nil\n" + generated)
			},
		} {
			L := NewState()
			err := run(L)
			if err == nil || !strings.Contains(err.Error(), "read-only table") && !strings.Contains(err.Error(), "protected metatable") {
				t.Errorf("%s (%s): got %v", source, name, err)
			}
			L.Close()
		}
	}
}

func TestMidnightModuleConst(t *testing.T) {
	L := NewState()
	defer L.Close()
	// (hoisted, like other module names)
	src := `module function check() { return x + y } module const x = 1 module const { y } = { y = 2 }`
	if err := L.DoMidnightString(src); err != nil {
		t.Fatal(err)
	}
	if got := midnightModuleCheck(t, L, src); got != "3" {
		t.Errorf("got %s", got)
	}
}

func TestMidnightConstErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{"const x = 1\nx = 2", `line:2(column:0) near '=':   cannot assign to const 'x' (declared on line 1)`},
		{`const x = 1; x += 1`, `cannot assign to const 'x'`},
		{`const x = 1; x ||= 1`, `cannot assign to const 'x'`},
		{`const x = 1; local y; y, x = 1, 2`, `cannot assign to const 'x'`},
		{`const { a, b } = {}; b = 1`, `cannot assign to const 'b'`},
		{`const [a] = {}; a = 1`, `cannot assign to const 'a'`},
		// from a closure
		{`const x = 1; local f = () -> { x = 2 }`, `cannot assign to const 'x'`},
		{`const f = () -> { f = nil }`, `cannot assign to const 'f'`},
		// function / class declarations assign the name
		{`const f = 1; function f() {}`, `cannot assign to const 'f'`},
		{`const A = 1; class A {}`, `cannot assign to const 'A'`},
		// module names, hoisted, and through module.x
		{`module const x = 1; module.x = 2`, `cannot assign to const 'x'`},
		{`module function f() { x = 2 } module const x = 1`, `cannot assign to const 'x'`},
		{`module function f() { module.x = 2 } module const x = 1`, `cannot assign to const 'x'`},
		// a value is required
		{`const x`, `const 'x' needs a value (ex. const x = ...)`},
		{`module const x`, `const 'x' needs a value`},
		{`local const x = 1`, `cannot use 'local const': use 'const' (or 'module const')`},
		{`global const x = 1`, `cannot use 'global const'`},
	} {
		_, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got  %v\n  want %q", c.source, err, c.want)
		}
	}
}
