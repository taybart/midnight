package lint

import (
	"fmt"
	"strings"
	"testing"

	"github.com/taybart/midnight/parse"
)

func check(t *testing.T, source string, opts Options) []string {
	t.Helper()
	chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(source), "<string>", parse.MidnightOptions{Match: true})
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
		chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(c.source), "<string>", parse.MidnightOptions{Markup: true})
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
