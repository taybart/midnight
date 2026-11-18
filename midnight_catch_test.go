package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

const catchPrelude = "local boom = (m) -> error(m, 0)\n"

// catch (see parse/midnight_catch.go). Each source (after catchPrelude)
// returns a string, checked natively and as `midnight compile` output.
var midnightCatchCases = []struct {
	source string
	want   string
}{
	// fallback values
	{`local v = boom('x') catch 'fb'; return v`, `fb`},
	{`local v = tonumber('7') catch 0; return tostring(v)`, `7`},
	{`local v = boom('x') catch nil; return tostring(v)`, `nil`},
	{`local v = boom('x') catch boom('y') catch 'c'; return v`, `c`}, // chains
	{`local v = 'ok' catch boom('y'); return v`, `ok`},               // the fallback is only evaluated on an error
	// the error, by name
	{`local v = boom('bad') catch e { "got " .. e }; return v`, `got bad`},
	{`local v = boom('x') catch { 'braced' }; return v`, `braced`},
	{`local v = boom('x') catch { {} }; return type(v)`, `table`},
	// protects the whole expression (looser than any operator)
	{`local t; local v = t.a.b catch 'idx'; return v`, `idx`},
	{`local v = 1 + nil catch 'arith'; return v`, `arith`},
	{`local t; local v = t.f(1) catch 'fn'; return v`, `fn`},
	{`local t; local v = t:m() catch 'method'; return v`, `method`},
	{`local o = { n = 'o', m = () => error(self.n, 0) }; return o:m() catch e { e }`, `o`},
	{`local v = 'a' .. 'b' catch 'no'; return v`, `ab`},
	{`local v = false || boom('x') catch 'or'; return v`, `or`},
	// statements that exit run in the enclosing function / loop
	{`local f = (s) -> { local v = (s || boom('missing')) catch e { return 'ret ' .. e }; return 'val ' .. v }; return f('a') .. ',' .. f(nil)`, `val a,ret missing`},
	{`local out = ''; for i = 1, 4 { local v = (i % 2 == 0 && boom('x') || i) catch { continue }; out ..= v }; return out`, `13`},
	{`local out = ''; for i = 1, 4 { local v = (i == 3 && boom('x') || i) catch { break }; out ..= v }; return out`, `12`},
	{`local f = () -> { local v = boom('inner') catch e { error('outer ' .. e, 0) }; return v }; return select(2, pcall(f))`, `outer inner`},
	{`local f = (x) -> { local v = boom('x') catch { if x { return 'a' } else { return 'b' } }; return v }; return f(true) .. f(false)`, `ab`},
	{`local f = () -> { return boom('x') catch e { return 'r' .. e } }; return f()`, `rx`},
	{`local f = () -> boom('x') catch e { error('arrow ' .. e, 0) }; return select(2, pcall(f))`, `arrow x`},
	{`local v; v = boom('x') catch { return 'assign' }; return v`, `assign`},
	// anywhere an expression goes
	{`return tostring(boom('x') catch 'arg')`, `arg`},
	{`local t = { a = boom('x') catch 1, boom('y') catch 2 }; return t.a .. t[1]`, `12`},
	{`return "s {boom('x') catch 'interp'}"`, `s interp`},
	{`return 'p' .. (boom('x') catch 'q')`, `pq`},
	{`return tostring(<b>{boom('x') catch 'markup'}</b>)`, `<b>markup</b>`},
	{`local v = if true { boom('x') catch 'in if' } else { '' }; return v`, `in if`},
	// one value
	{`local two = () -> (1, 2); return select('#', two() catch 0)`, `1`},
	{`local f = (...) -> { return select('#', ... catch 0) }; return tostring(f('a', 'b'))`, `1`},
	{`local f = (...) -> { local v = boom(...) catch e { e }; return v }; return f('va')`, `va`},
	{`local f = (...) -> { local v = (... .. '!') catch 'none'; return v }; return f('a') .. f()`, `a!none`},
	// headers: a catch there has no body
	{`if boom('x') catch false { return 'wrong' } else { return 'if' }`, `if`},
	{`local i = 0; while (i < 2 && boom('x')) catch false { i += 1 }; return tostring(i)`, `0`},
	{`local out = ''; for _, v in ipairs(boom('x') catch { { 'a', 'b' } }) { out ..= v }; return out`, `ab`},
	{`local fb = 'm'; return match boom('x') catch fb { 'm' => { 'match' } _ => { '?' } }`, `match`},
	{`local fb = 1; if boom('x') catch fb { return 'header name' }; return ''`, `header name`},
	// statements
	{`local r = 'no'; boom('x') catch { r = 'yes' }; return r`, `yes`},
	{`local r; boom('stmt') catch e { r = e }; return r`, `stmt`},
	{`boom('x') catch {}; return 'ignored'`, `ignored`},
	{`local r = 'untouched'; print('') catch { r = 'no' }; return r`, `untouched`},
	{`local f = () -> { boom('x') catch { return 'early' }; return 'late' }; return f()`, `early`},
	// `catch` is still a name where it can't be a catch
	{`local catch = (x) -> x .. '!'; return catch('a')`, `a!`},
	{"local catch = 1\nlocal y = catch\nreturn tostring(y)", `1`},
	{"local catch = {}\nlocal y = 1\ncatch.x = 2\nreturn tostring(catch.x)", `2`},
	{"local f = (x) -> x\nlocal y = f(1)\ncatch = 5\nreturn tostring(catch)", `5`},
	{"local catch = (x) -> x\nlocal y = 1\ncatch(3)\nreturn tostring(y)", `1`},
	{`local t = { catch = 'c' }; return t.catch`, `c`},
	// on the next line, it's a catch when it can't be a name
	{"local v = boom('x')\n  catch e { 'next line ' .. e }\nreturn v", `next line x`},
}

func TestMidnightCatch(t *testing.T) {
	for _, c := range midnightCatchCases {
		source := catchPrelude + c.source
		chunk, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
		if err != nil {
			t.Errorf("%s\n%v", c.source, err)
			continue
		}
		generated := luagen.Generate(chunk)
		for _, run := range []struct {
			name string
			fn   func(L *LState) error
		}{
			{"native", func(L *LState) error { return L.DoMidnightString(source) }},
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

// How many functions catches make: none when the protected expression is a
// call of a name w/ simple arguments, and the catch is the value of a
// declaration, assignment, or return.
func TestMidnightCatchLowering(t *testing.T) {
	for _, c := range []struct {
		source string
		fns    int
	}{
		{`local v = f(a, 1, 'x') catch 0`, 0},
		{`v = f() catch e { e }`, 0},
		{`local v = f() catch g() catch 0`, 1}, // the first catch, in a function
		{`local v = t.f() catch 0`, 1},
		{`local v = f(t.a) catch 0`, 1},
		{`local v = f() catch e { return nil, e }`, 0},
		{`local v = f() catch if a { 1 } else { 2 }`, 0},
		{`f() catch {}`, 0},
		{`f(x) catch e { log(e) }`, 0},
		{`g(f() catch 0)`, 1},
	} {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)
		if fns := strings.Count(generated, "function"); fns != c.fns {
			t.Errorf("%s\nmade %d functions, want %d:\n%s", c.source, fns, c.fns, generated)
		}
	}
}

func TestMidnightCatchErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`local v = f() catch e { log(e) local x = 1 }`, `must end in 'return', 'break', 'continue', 'goto', or 'error(...)'`},
		{`local v = f() catch { local x = 1 }`, `must end in 'return'`},
		{`local v = f() catch {}`, `expected the catch's value, got '}'`},
		{`g(f() catch e { return e })`, `only works as the value of a declaration, assignment, or return`},
		{`local a, b = 1, f() catch { return }`, `only works as the value of a declaration`},
		{`local x = 0; x += f() catch { return }`, `only works as the value of a declaration`},
		{`local v = f() catch e { return } catch 0`, `only works as the value of a declaration`},
		{`f() catch 0`, `a catch statement needs a block`},
		{`local v = f() catch { 'a', 'b' }`, "for a table, use `catch { { ... } }`"},
		{`local v = f() catch`, `unexpected eof`},
		{`local v = f() catch { break }`, `cannot use 'break' outside of loop`},
		{`local v = f() catch { continue }`, `cannot use 'continue' outside of loop`},
	} {
		_, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got  %v\n  want %q", c.source, err, c.want)
		}
	}
}

func TestMidnightCatchFormat(t *testing.T) {
	src := `local cfg = load_config(path) catch DEFAULTS
local data = json.decode(body) catch e { return nil, e }
local n = tonumber(s) catch e { log(e) }
risky() catch e {
  log.warn(e)
}
risky() catch {}
`
	out, err := format.Format([]byte(src), format.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("fmt changed it:\n%s", out)
	}
}
