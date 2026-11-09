package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// if / match expressions (see parse/midnight_cond.go). Each source returns a
// string, checked natively and as `midnight compile` output.
var midnightCondCases = []struct {
	source string
	want   string
}{
	// the branch taken is the value, even when it's nil (unlike `c && a || b`)
	{`local admin, title = true, nil; local v = if admin { title } else { 'member' }; return tostring(v)`, `nil`},
	{`local v = if false { 1 } else { 2 }; return tostring(v)`, `2`},
	{`local f = (n) -> if n > 1 { 'many' } elseif n == 1 { 'one' } else { 'none' }; return f(2) .. f(1) .. f(0)`, `manyonenone`},
	{`local f = (n) -> { return if n { 'y' } else { 'n' } }; return f(true) .. f(false)`, `yn`},
	// match
	{`local f = (k) -> match k { 'dir' => { 'D' } 'link' | 'sub' => { 'L' } _ => { 'F' } }; return f('dir') .. f('sub') .. f('x')`, `DLF`},
	{`local n = 7; return match n { 1 => { 'one' } _ if n > 5 => { 'big' } _ => { 'small' } }`, `big`},
	{`local calls = 0; local s = () -> { calls += 1; return 2 }; local v = match s() { 1 => { 'a' } 2 => { 'b' } _ => { 'c' } }; return v .. calls`, `b1`},
	{`return match 1 { _ => { 'only' } }`, `only`},
	// anywhere an expression goes
	{`local t = { if true { 'a' } else { 'b' }, match 3 { 1 => { 'x' } _ => { 'y' } } }; return t[1] .. t[2]`, `ay`},
	{`local f = (a, b) -> a .. b; return f(if true { 'x' } else { 'y' }, 'z')`, `xz`},
	{`return 'n=' .. (if true { 1 } else { 2 })`, `n=1`},
	{`local t = {}; t.a = if true { 'set' } else { 'no' }; return t.a`, `set`},
	{`local a; a = match 2 { 2 => { 'two' } _ => { '?' } }; return a`, `two`},
	{`local x = 5; local x = if true { x } else { 0 }; return tostring(x)`, `5`}, // the outer x
	{`local { a, b } = if true { { a = 1, b = 2 } } else { {} }; return a .. b`, `12`},
	// nested (lowered into the same if chain)
	{`local v = if false { 1 } else { if true { match 'q' { 'q' => { 'deep' } _ => { '' } } } else { 3 } }; return v`, `deep`},
	// branches are one value
	{`local two = () -> (1, 2); return select('#', if true { two() } else { 0 })`, `1`},
	{`local f = (...) -> { return if true { ... } else { 0 } }; return select('#', f('a', 'b')) .. f('a', 'b')`, `1a`},
	{`local f = (...) -> { local t = { if true { ... } else { 0 } }; return #t .. t[1] }; return f('v', 'w')`, `1v`},
	// evaluation order is kept
	{`local log = ''; local p = (s) -> { log ..= s; return s }; local a, b = if true { p('1') } else { '' }, p('2'); return log`, `12`},
	// fat arrow functions in branches (and `self`)
	{`local o = { n = 'o', f = if true { () => self.n } else { nil } }; return o:f()`, `o`},
	// markup
	{`local c = { current = false, name = 'x', path = 'p/x' }; return tostring(<span>{if c.current { c.name } else { <a href="/{c.path}">{c.name}</a> }}</span>)`, `<span><a href="/p/x">x</a></span>`},
	// `match` is still a name where it can't be a match expression
	{`local match = (x) -> x .. '!'; return match('a')`, `a!`},
	{`local match = 1; return tostring(match + 1)`, `2`},
	{`return ('abc'):match('b')`, `b`},
	{`local s = 'abc'; return s:match('c') .. string.match(s, 'a')`, `ca`},
	{`local t = { match = 'm' }; return t.match`, `m`},
	// statements are unchanged
	{`local r = ''; if true { r = 'stmt' } else { r = 'no' }; return r`, `stmt`},
	{`local r = ''; match 2 { 2 => { r = 'stmt' } }; return r`, `stmt`},
}

func TestMidnightCond(t *testing.T) {
	for _, c := range midnightCondCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Errorf("%s\n%v", c.source, err)
			continue
		}
		generated := luagen.Generate(chunk)
		for _, run := range []struct {
			name string
			fn   func(L *LState) error
		}{
			{"native", func(L *LState) error { return L.DoMidnightString(c.source) }},
			{"luagen", func(L *LState) error { return L.DoString(generated) }},
		} {
			L := NewState()
			if err := run.fn(L); err != nil {
				t.Errorf("%s (%s)\n%s\n%v", c.source, run.name, generated, err)
			} else if got := L.Get(-1).String(); got != c.want {
				t.Errorf("%s (%s)\n%s\n  got  %s\n  want %s", c.source, run.name, generated, got, c.want)
			}
			L.Close()
		}
	}
}

// As the value of a declaration, assignment, or return, they're statements:
// no function is made.
func TestMidnightCondLowering(t *testing.T) {
	for _, source := range []string{
		`local v = if a { 1 } elseif b { 2 } else { 3 }`,
		`local v = match k { 1 => { 'a' } _ => { 'b' } }`,
		`v = if a { 1 } else { 2 }`,
		`local f = (x) -> if x { 1 } else { 2 }`,
		`local f = (x) -> { return match x { 1 => { 1 } _ => { 2 } } }`,
		`local v = if a { if b { 1 } else { match c { 1 => { 2 } _ => { 3 } } } } else { 4 }`,
	} {
		chunk, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", source, err)
		}
		generated := luagen.Generate(chunk)
		fns := strings.Count(generated, "function")
		if strings.Contains(source, "->") {
			fns-- // the arrow function itself
		}
		if fns != 0 {
			t.Errorf("%s\nmade a function:\n%s", source, generated)
		}
	}
}

func TestMidnightCondErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`local v = if a { 1 }`, `an if expression needs an 'else'`},
		{`local v = if a { 1 } elseif b { 2 }`, `an if expression needs an 'else'`},
		{`local v = if a { 1 } else if b { 2 } else { 3 }`, `did you mean 'elseif'?`},
		{`local v = match k { 1 => { 'a' } }`, `a match expression needs a '_ =>' arm`},
		{`local v = match k { _ => { 'a' } 1 => { 'b' } }`, `unreachable match arm`},
		{`local v = if a { local x = 1 } else { 2 }`, `a branch of an if / match expression is one expression, not statements`},
		{`local v = if a { return 1 } else { 2 }`, `not statements (use an if statement)`},
		{`local v = if a { f() g() } else { 2 }`, `a branch of an if / match expression is one expression`},
		{`local v = if a { } else { 2 }`, `expected an expression (the branch's value)`},
	} {
		_, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got  %v\n  want %q", c.source, err, c.want)
		}
	}
}

func TestMidnightCondFormat(t *testing.T) {
	src := `local label = if user.admin { user.title } else { 'member' }
local icon = match entry.kind {
  'dir' => { 'D' }
  'link' | 'submodule' => { 'L' }
  _ => { 'F' }
}
return <span>{if c.current { c.name } else { <a href="/{c.path}">{c.name}</a> }}</span>
`
	out, err := format.Format([]byte(src), format.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("fmt changed it:\n%s", out)
	}
}
