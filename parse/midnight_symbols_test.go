package parse

import (
	"regexp"
	"strings"
	"testing"
)

// occurrences returns the offsets of name as a whole word in src.
func occurrences(src, name string) []int {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	var offsets []int
	for _, m := range re.FindAllStringIndex(src, -1) {
		offsets = append(offsets, m[0])
	}
	return offsets
}

func TestMidnightSymbols(t *testing.T) {
	tests := []struct {
		name, src, ident string
		// occurrence (of ident) -> occurrence of its declaration, or -1 for
		// none (a global / builtin)
		want map[int]int
	}{
		{"local", "local x = 1\nprint(x)", "x", map[int]int{1: 0}},
		{"shadow", "local x = 1\nif true {\n  local x = 2\n  print(x)\n}\nprint(x)", "x",
			map[int]int{2: 1, 3: 0}},
		{"self reference", "local x = 1\nif true {\n  local x = x + 1\n}", "x", map[int]int{2: 0}},
		{"local function recursion", "local function f(n) { return f(n - 1) }", "f", map[int]int{1: 0}},
		{"params", "local function f(a, b) { return a + b }", "b", map[int]int{1: 0}},
		{"arrow", "local g = (a) -> a * 2\nlocal h = a -> a", "a", map[int]int{1: 0, 3: 2}},
		{"global", "print(y)\nglobal y = 1\nprint(y)", "y", map[int]int{0: 1, 2: 1}},
		{"undeclared", "print(z)", "print", map[int]int{0: -1}},
		{"module hoisted", "module function g() { return h() }\nmodule function h() { return 1 }", "h",
			map[int]int{0: 1}},
		{"module access", "module x = 1\nlocal function f(x) { return module.x + x }", "x",
			map[int]int{2: 0, 3: 1}},
		{"destructure", "local t = {}\nconst { a, b: c } = t\nprint(a, c)", "c", map[int]int{1: 0}},
		{"array destructure", "local [first, second] = {1, 2}\nprint(second)", "second", map[int]int{1: 0}},
		{"for", "for i, v in ipairs({}) { print(i, v) }\nfor k = 1, 3 { print(k) }", "v", map[int]int{1: 0}},
		{"numeric for", "for k = 1, 3 { print(k) }", "k", map[int]int{1: 0}},
		{"interpolation", "local name = 'x'\nprint(\"hi {name}\")", "name", map[int]int{1: 0}},
		{"markup component", "local Card = (p) -> <div>{p.title}</div>\nlocal v = <Card title=\"x\"/>", "Card",
			map[int]int{1: 0}},
		{"class", "local class Animal {}\nlocal class Dog : Animal {}\nlocal d = Dog()", "Animal",
			map[int]int{1: 0}},
		{"catch", "local n = tonumber(\"1\") catch e { print(e) }", "e", map[int]int{1: 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			syms := &Symbols{}
			if _, err := ParseMidnightWithOptions(strings.NewReader(tt.src), "<test>", MidnightOptions{Symbols: syms}); err != nil {
				t.Fatal(err)
			}
			offsets := occurrences(tt.src, tt.ident)
			for use, decl := range tt.want {
				got := syms.DeclAt(offsets[use])
				switch {
				case decl < 0 && got != nil:
					t.Errorf("%s #%d: got decl at %d, want none", tt.ident, use, got.Start)
				case decl >= 0 && got == nil:
					t.Errorf("%s #%d: no decl, want %d", tt.ident, use, offsets[decl])
				case decl >= 0 && got.Start != offsets[decl]:
					t.Errorf("%s #%d: decl at %d, want %d", tt.ident, use, got.Start, offsets[decl])
				}
			}
		})
	}
}

func TestMidnightSymbolsPartialOnError(t *testing.T) {
	syms := &Symbols{}
	src := "local x = 1\nprint(x)\nlocal = oops"
	if _, err := ParseMidnightWithOptions(strings.NewReader(src), "<test>", MidnightOptions{Symbols: syms}); err == nil {
		t.Fatal("expected error")
	}
	if d := syms.DeclAt(strings.Index(src, "(x)") + 1); d == nil || d.Name != "x" {
		t.Errorf("want x's decl before the error, got %+v", d)
	}
}

func TestMidnightSymbolsRequire(t *testing.T) {
	src := "const m = require('lib.m')\n" +
		"local { render, json: j } = require(\"midnight.markup\")\n" +
		"local [a] = require('arr')\n" +
		"local n, o = require('n'), 1\n" +
		"print(m.bar, m().baz, j)\n"
	syms := &Symbols{}
	if _, err := ParseMidnightWithOptions(strings.NewReader(src), "<test>", MidnightOptions{Symbols: syms}); err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		"m": {"lib.m", ""}, "render": {"midnight.markup", "render"}, "j": {"midnight.markup", "json"},
		"a": {"", ""}, "n": {"n", ""}, "o": {"", ""},
	}
	for _, d := range syms.Decls {
		if w := want[d.Name]; d.Require != w[0] || d.Key != w[1] {
			t.Errorf("%s: got require %q key %q, want %q %q", d.Name, d.Require, d.Key, w[0], w[1])
		}
	}

	f := syms.FieldAt(strings.Index(src, "bar"))
	if f == nil || f.Name != "bar" || syms.Decls[syms.Refs[f.Ref].Decl].Name != "m" {
		t.Errorf("m.bar: got %+v", f)
	}
	if f := syms.FieldAt(strings.Index(src, "baz")); f != nil {
		t.Errorf("m().baz isn't a field of m, got %+v", f)
	}
}
