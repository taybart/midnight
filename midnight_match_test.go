package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

var midnightMatchOptions = parse.MidnightOptions{Match: true}

const midnightMatchKind = `
local MOVIE = 'movie'
local function kind(mode, count = 0) {
  match mode {
    MOVIE => { return 'film' }
    'show' | 'series' => {
      return 'tv'
    }
    _ if count > 5 => { return 'many' },
    _ => { return 'other' }
  }
}
`

var midnightMatchCases = []struct {
	source string
	want   string
}{
	{midnightMatchKind + `return kind('movie') .. kind('series') .. kind('x', 10) .. kind('x')`, "filmtvmanyother"},
	{`local r = 0; match 3 { 1 | 2 => { r = 1 } 3 => { r = 3 } }; return r`, "3"},
	{`local r = 'none'; match 'x' { 'a' => { r = 'a' } }; return r`, "none"},
	{`local r; match 1 { _ => { r = 'all' } }; return r`, "all"},
	{`local r = 0; match 1 {}; return r`, "0"},
	// subject is evaluated once
	{`local n = 0; local function f() { n += 1; return 2 } match f() { 1 => {} 2 => {} 3 => {} }; return n`, "1"},
	// break / continue apply to the enclosing loop
	{`local s = 0; for i = 1, 6 { match i % 3 { 0 => { continue } 1 => { s += i } } s += 100 }; return s`, "405"},
	{`local s = 0; while true { s += 1; match s { 3 => { break } } }; return s`, "3"},
	// patterns: parens, arrow functions, `|` needs parens to mean bitwise or
	{`local r; match 3 { (1 | 2) => { r = 'bor' } }; return r`, "bor"},
	{`local r; local t = { x = 1 }; match 1 { (t.x) => { r = 'paren' } }; return r`, "paren"},
	{`local r; local f = g -> g(1); match 2 { f(x -> x + 1) => { r = 'arrow' } }; return r`, "arrow"},
	{`local r; local a = 1; match 1 { a => { r = 'name' } }; return r`, "name"},
	// subjects that look like they continue a name
	{`local r; local x = 2; match (x) { 2 => { r = 'paren' } }; return r`, "paren"},
	{`local r; local x = 2; match -x { -2 => { r = 'neg' } }; return r`, "neg"},
	{`local r; match #'ab' { 2 => { r = 'len' } }; return r`, "len"},
	// `match` is still a name everywhere else
	{`return ('a1'):match('%d')`, "1"},
	{`return string.match('b2', '%d')`, "2"},
	{`local t = { match = 3 }; return t.match`, "3"},
	{`local match = 1; match = 4; match += 1; return match`, "5"},
	{`local match, y = 1, 2; match, y = y, match; return match`, "2"},
	{`local r; local function match(x) { r = x } match(6); match (7); return r`, "7"},
	{`local match = { x = 0 }; match.x = 8; match['x'] += 1; return match.x`, "9"},
	{`local match = { v = 10, get = self -> self.v }; match:get(); return match:get()`, "10"},
	{`local r = 0; local match = 11; match ~= 1; return match`, "10"},
}

func TestMidnightMatch(t *testing.T) {
	for _, c := range midnightMatchCases {
		L := NewState(Options{Midnight: midnightMatchOptions})
		if err := L.DoMidnightString(c.source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n  got %s, want %s", c.source, got, c.want)
		}
		L.Close()
	}
}

func TestLuagenMidnightMatch(t *testing.T) {
	for _, c := range midnightMatchCases {
		chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(c.source), "<string>", midnightMatchOptions)
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n%s\n  got %s, want %s", c.source, generated, got, c.want)
		}
		L.Close()
	}
}

func TestMidnightMatchSyntaxErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"match x {\n  1 => {", "at EOF"},
		{"match x {", "at EOF"},
		{`match x { 1 { } }`, "expected '=>' got '{'"},
		{`match x { _ => {} 1 => {} }`, "unreachable match arm"},
	}

	for _, c := range cases {
		L := NewState(Options{Midnight: midnightMatchOptions})
		_, err := L.LoadMidnightString(c.source)
		L.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got %v, want error containing %q", c.source, err, c.want)
		}
	}
}

// Without the experiment, `match` is an ordinary name (same as the reference
// compiler).
func TestMidnightMatchDisabled(t *testing.T) {
	if got := midnightEval(t, `local match = x -> x + 1; return match(1)`); got.String() != "2" {
		t.Errorf("got %s, want 2", got)
	}

	L := NewState()
	defer L.Close()
	if _, err := L.LoadMidnightString(`match x { 1 => {} }`); err == nil {
		t.Error("match statement parsed w/o the match experiment")
	}
}

// Options apply to required modules too.
func TestMidnightMatchRequire(t *testing.T) {
	dir := t.TempDir()
	module := "return x -> { match x { 1 => { return 'one' } _ => { return 'other' } } }\n"
	if err := os.WriteFile(filepath.Join(dir, "kind.mn"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	L := NewState(Options{Midnight: midnightMatchOptions})
	defer L.Close()
	L.SetField(L.GetField(L.Get(GlobalsIndex), "package"), "path", LString(filepath.Join(dir, "?.mn")))

	if err := L.DoMidnightString(`return require('kind')(1)`); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "one" {
		t.Errorf("got %q, want %q", got, "one")
	}
}
