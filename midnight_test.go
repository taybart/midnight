package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func midnightEval(t *testing.T, source string) LValue {
	t.Helper()
	L := NewState()
	defer L.Close()
	if err := L.DoMidnightString(source); err != nil {
		t.Fatalf("%s\n%v", source, err)
	}
	return L.Get(-1)
}

var midnightEvalCases = []struct {
	source string
	want   string
}{
	{`return 1 + 2 * 3`, "7"},
	{`return 2 ^ 3 ^ 2`, "512"},
	{`return 7 // 2`, "3"},
	{`return (5 ~ 3) | (1 << 4) & 0xFF`, "22"},
	{`return ~0`, "4294967295"},
	{`return !true || 1 != 2 && 'yes'`, "yes"},
	{`local a = 'b'; return "a{a}c\{d\}"`, "abc{d}"},
	{"return [[\nx{1 + 1}\ny]]", "x2\ny"},
	{`return 'abc':upper()`, "ABC"},
	{`return 010`, "10"},
	{`local [a, b = 2] = { 1 }; return a + b`, "3"},
	{`local { a, b: c = 3 } = { a = 1 }; return a + c`, "4"},
	{`local f = (a, b = 1, ...rest) -> a + b + #rest; return f(1, nil, 0, 0)`, "4"},
	{`local f = { x = 2, g = y => self.x * y }; return f:g(3)`, "6"},
	{`local f = () -> (1, 2); local a, b = f(); return a + b`, "3"},
	{`local t = { a = { 1 } }; t.a[1] += 4; t.a[1] ..= '!'; return t.a[1]`, "5!"},
	{`local n = 0; local function k() { n += 1; return 1 } local t = { 1 }; t[k()] *= 3; return n * 10 + t[1]`, "13"},
	{`local a, b = 1, 2; a, b += 10, 20; return a + b`, "33"},
	{`local s = 0; for i = 1, 10 { if i % 2 == 0 { continue } s += i }; return s`, "25"},
	{`local s = ''; for _, { x } in ipairs({ { x = 'a' }, { x = 'b' } }) { s ..= x }; return s`, "ab"},
	{`global g = 5; local r = g; _G.g = nil; return r`, "5"},
	{`module x = 1; module function f() { return x + 1 }`, "table"},
	{`local end = 1; local t = { not = 2 }; return end + t.not`, "3"},
	{`local t = { end = (self, x) -> x + 1 }; local u = { t = t }; return t:end(1) + u.t:end(2)`, "5"},
	{`global then = 4; local r = then; _G['then'] = nil; return r`, "4"},
	{`local s = 0; for i = 1, 5 { i *= 2; s += i }; return s`, "30"},
	{`local s = 0; while true { s += 1; if s > 2 { break } s += 10 }; return s`, "12"},
	{`local x = - -1; return -x ^ 2`, "-1"},
	{`local a = 1; ('x'):rep(2); return (a)`, "1"},
}

func TestMidnightEval(t *testing.T) {
	for _, c := range midnightEvalCases {
		got := midnightEval(t, c.source)
		if got.Type() == LTTable {
			if c.want != "table" {
				t.Errorf("%s\n  got table, want %s", c.source, c.want)
			}
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s\n  got %s, want %s", c.source, got.String(), c.want)
		}
	}
}

func TestMidnightSyntaxErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"local x = 1\nlocal = 2", "line:2"},
		{"local x = 1\nlocal y = ", "at EOF"},
		{"if x {\n  print(x)", "at EOF"},
		{"local s = [[abc", "at EOF"},
		{`return x ~= 1`, "did you mean '!='?"},
		{`break`, "cannot use 'break' outside of loop"},
		{`local f = () -> ...`, "cannot use '...' outside a vararg function"},
		{`if true { module x = 1 }`, "module declarations must appear at the top level"},
		{`module x = 1; return x`, "cannot use 'return' w/ 'module' declarations"},
		{`local s = "abc`, "unterminated string"},
		{`f() = 1`, "unexpected token '='"},
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

func TestMidnightRuntimeErrorLine(t *testing.T) {
	L := NewState()
	defer L.Close()
	err := L.DoMidnightString("local t = {}\n\nreturn t.a.b")
	if err == nil || !strings.Contains(err.Error(), "<string>:3:") {
		t.Errorf("got %v, want error on line 3", err)
	}
}

func TestMidnightRequire(t *testing.T) {
	dir := t.TempDir()
	module := "module greeting = 'hi'\nmodule function greet(name) { return \"{greeting} {name}\" }\n"
	if err := os.WriteFile(filepath.Join(dir, "greeter.mn"), []byte(module), 0o644); err != nil {
		t.Fatal(err)
	}

	L := NewState()
	defer L.Close()
	L.SetField(L.GetField(L.Get(GlobalsIndex), "package"), "path", LString(filepath.Join(dir, "?.mn")))

	if err := L.DoString(`return require('greeter').greet('midnight')`); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "hi midnight" {
		t.Errorf("got %q, want %q", got, "hi midnight")
	}
}

// `.lua` patterns in package.path also find `.mn` modules, so a custom
// LUA_PATH (w/o `?.mn`) still finds them.
func TestMidnightRequireLuaPattern(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "mod.mn"), []byte("return x -> x * 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	L := NewState()
	defer L.Close()
	L.SetField(L.GetField(L.Get(GlobalsIndex), "package"), "path", LString(filepath.Join(dir, "?.lua")))

	if err := L.DoMidnightString(`return require('mod')(21)`); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "42" {
		t.Errorf("got %s, want 42", got)
	}
}
