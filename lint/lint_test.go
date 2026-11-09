package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/taybart/midnight/parse"
)

func check(t *testing.T, source string, opts Options) []string {
	t.Helper()
	chunk, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
	if err != nil {
		t.Fatalf("%s\n%v", source, err)
	}
	var got []string
	for _, d := range Check(chunk, opts) {
		got = append(got, fmt.Sprintf("%d:%s", d.Line, d.Rule))
	}
	return got
}

func TestLint(t *testing.T) {
	cases := []struct {
		source string
		want   []string
	}{
		// a && b || c where b can be nil or false
		{"local a, b = true, nil\nprint(a && b || 'c')", []string{"2:and-or"}},
		{"local a, t = true, {}\nprint(a && t.name || 'c')", []string{"2:and-or"}},
		{"local a, f = true, () -> 1\nprint(a && f() || 'c')", []string{"2:and-or"}},
		{"local a, x = true, 1\nprint(a && x == 1 || 'c')", []string{"2:and-or"}}, // false is falsy too
		{"local a = true\nprint(a && nil || 'c')", []string{"2:and-or"}},
		{"local a = true\nprint(a && false || 'c')", []string{"2:and-or"}},
		// ... but not when b can't be
		{"local a = true\nprint(a && 'x' || 'c', a && 1 || 2, a && {} || {}, a && true || false)", nil},
		// safe navigation: falling back when b is nil is the point
		{"local x, f = {}, (v) -> v\nprint(x && x.y || 'd', x && x.y.z || 'd', x && x:m() || 'd', x && f(x) || 0, x != nil && x.y || 'd')", nil},
		{"local c = {}\nprint(c.a && c.a.b || 'd')", nil},
		// ... but a different field is still reported
		{"local c = {}\nprint(c.current && c.name || 'link')", []string{"2:and-or"}},
		// standard functions that always return a value
		{"local n, s = 1, 's'\nprint(n > 0 && math.floor(n) || 0, n && tostring(n) || '', s && string.format('%s', s) || '')", nil},
		// a nil / false fallback is what a falsy b would give anyway
		{"local a, b = true, nil\nprint(a && b || nil, a && b || false)", nil},
		{"local a, n, s = true, 2, 's'\nprint(a && n + 1 || 0, a && s .. '!' || '', a && #s || 0, a && -n || 0)", nil},
		{"local a, n = true, nil\nprint(a && (n || 'default') || 'c')", nil},
		{"local a = true\nprint(a && <b/> || <i/>, a && (() -> 1) || nil)", nil},
		{"local a, b = true, nil\nprint(a && b, a || b, if a { b } else { 'c' })", nil},
		// (match patterns and guards lower to and / or, and aren't reported)
		{"local k, g = 1, true\nmatch k { 1 | 2 if g => { print(k) } _ => {} }", nil},
		// unused locals
		{"local x = 1", []string{"1:unused-local"}},
		{"local x = 1\nprint(x)", nil},
		{"local _x, _ = 1, 2", nil},
		{"local x\nx = 1", []string{"1:unused-local"}},
		{"local x = 1\nx += 1", nil}, // reads x
		{"local function f() {}", []string{"1:unused-local"}},
		{"local function f(n) { return n > 0 && f(n - 1) }\nprint(f(1))", nil},
		{"local { a, b } = {}\nprint(a)", []string{"1:unused-local"}},
		{"local [a] = {}\nprint(a)", nil},
		{"print((x, y) -> x)", nil}, // params aren't reported
		{"for i, v in ipairs({}) {}", nil},

		// globals
		{"print(nope)", []string{"1:undefined-global"}},
		{"nope = 1", []string{"1:set-global"}},
		{"nope = 1\nprint(nope)", []string{"1:set-global"}},
		{"function nope() {}", []string{"1:set-global"}},
		{"global g = 1\nprint(g)", nil},
		{"global function g() {}\ng()", nil},
		{"module m = 1", nil},
		{"print(string, _G, bit32)", nil},
		{"print(host)\nhost = 1", []string{"2:set-global"}},
		{"local t = {}\nfunction t.f() {}\nfunction t:g() { return self }", nil},

		// shadowing / redefinition
		{"local x = 1\ndo { local x = 2; print(x) }\nprint(x)", []string{"2:shadow"}},
		{"local x = 1\nprint(x)\nlocal x = 2\nprint(x)", []string{"3:redefined"}},
		{"local x = 1\nprint((x) -> x)\nprint(x)", []string{"2:shadow"}},
		{"local t = { f = () => () => self }\nprint(t)", nil}, // implicit self

		// unreachable
		{"while true {\n  break\n  print(1)\n}", []string{"3:unreachable"}},
		{"for i = 1, 2 {\n  if i == 1 { continue }\n  print(i)\n}", nil},
		{"for i = 1, 2 { continue }", nil}, // continue's label isn't code

		// Midnight lowering doesn't leak (temporaries, match, destructuring)
		{"local s = 'a'\nmatch s { 'a' => { print(s:match('a')) } _ => {} }", nil},
		{"local f = ({ a } = {}, [b] = {}) -> a + b\nprint(f)", nil},
		{"local t = { a = { 1 } }\nt.a[1] += 1", nil},
	}

	for _, c := range cases {
		got := check(t, c.source, Options{})
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s\n  got %v, want %v", c.source, got, c.want)
		}
	}
}

func TestLintGlobalsOption(t *testing.T) {
	got := check(t, "rest.label('x')\ncli = 1", Options{Globals: []string{"rest", "cli"}})
	if len(got) != 0 {
		t.Errorf("got %v, want no diagnostics", got)
	}
}

func TestLintFatArrowComponent(t *testing.T) {
	cases := []struct {
		source string
		want   []string
	}{
		{"return ({ item }) => <li>{item}</li>", []string{"1:fat-arrow-component"}},
		{"return (props) => {\n  local x = <p>{props}</p>\n  return x\n}", []string{"1:fat-arrow-component"}},
		{"return ({ item }) -> <li>{item}</li>", nil},
		{"return () => self.x", nil},                // no markup
		{"return () => <p>{self.name}</p>", nil},    // a method, uses self
		{"return () => { return () -> <p/> }", nil}, // the markup is in a nested function
	}
	for _, c := range cases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		var got []string
		for _, d := range Check(chunk, Options{}) {
			if d.Rule == RuleFatArrowComponent {
				got = append(got, fmt.Sprintf("%d:%s", d.Line, d.Rule))
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Errorf("%s\ngot %v, want %v", c.source, got, c.want)
		}
	}
}
