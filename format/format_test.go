package format

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/taybart/midnight/parse"
)

// Formats every valid spec snippet (see testdata/midnight-spec). Format itself
// checks the output parses to the same program, so this checks that never
// fails, and that formatting is idempotent.
func TestFormatSpecSnippets(t *testing.T) {
	paths, err := filepath.Glob("../testdata/midnight-spec/valid/*.mn")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no spec snippets found (%v)", err)
	}

	for _, path := range paths {
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		testFormat(t, path, source, Options{})
	}
}

func testFormat(t *testing.T, name string, source []byte, opts Options) []byte {
	t.Helper()
	once, err := Format(source, opts)
	if err != nil {
		t.Errorf("%s: %v\n%s", name, err, source)
		return nil
	}
	twice, err := Format(once, opts)
	if err != nil {
		t.Errorf("%s (2nd pass): %v\n%s", name, err, once)
		return nil
	}
	if string(once) != string(twice) {
		t.Errorf("%s: not idempotent\n--- once\n%s\n--- twice\n%s", name, once, twice)
	}
	return once
}

func TestFormatStyle(t *testing.T) {
	cases := []struct{ in, want string }{
		// spacing
		{"local x=1+2*3", "local x = 1 + 2 * 3"},
		{"x+=1\nx ..= 'a'\nx ~= 1", "x += 1\nx ..= 'a'\nx ~= 1"},
		{"local y = -x + - 1\nlocal z = a - -b\nreturn !x, #t, ~y", "local y = -x + -1\nlocal z = a - -b\nreturn !x, #t, ~y"},
		{"f( a,b )\nx = t [ 1 ]\nlocal [ a ] = t", "f(a, b)\nx = t[1]\nlocal [a] = t"},
		{"local t = {a=1,b={}}", "local t = { a = 1, b = {} }"},
		{"local f = (a,...rest)->a\nlocal g = x=>x", "local f = (a, ...rest) -> a\nlocal g = x => x"},
		{"s:upper()\nlocal { a: b } = t", "s:upper()\nlocal { a: b } = t"},
		{"if (x) { return (1, 2) }", "if (x) { return (1, 2) }"},
		{"goto done\n::done::", "goto done\n::done::"},
		{"print(x)--note\n--[[ block ]]", "print(x) -- note\n--[[ block ]]"},

		// trailing comments: aligned runs stay aligned, others get one space
		{"f(1)        -- a\nf(1, 2)     -- b", "f(1)        -- a\nf(1, 2)     -- b"},
		{"f(1)        -- a\nf(1,2,3,4)  -- b", "f(1)          -- a\nf(1, 2, 3, 4) -- b"},
		{"f(1)    -- a\nf(2)    -- b\n\nf(3)    -- c", "f(1)    -- a\nf(2)    -- b\n\nf(3) -- c"},
		{"f(1)    -- a\nf(2)  -- b", "f(1) -- a\nf(2) -- b"},
		{"if x {\n\tf(1)    -- a\n\tf(22)   -- b\n}", "if x {\n  f(1)    -- a\n  f(22)   -- b\n}"},

		// quotes (interpolation and escapes are left alone)
		{`print("a", "it's", "{x}", "a\"b")`, `print('a', "it's", "{x}", "a\"b")`},

		// indentation
		{"if x {\nprint(1)\n}", "if x {\n  print(1)\n}"},
		{"f({\na = 1,\n})", "f({\n  a = 1,\n})"},
		{"xpcall(() -> {\nf()\n}, g)", "xpcall(() -> {\n  f()\n}, g)"},
		{"if x {\n} else {\ny()\n}", "if x {\n} else {\n  y()\n}"},
		{"local s = a ..\nb ..\nc", "local s = a ..\n  b ..\n  c"},
		{"local s = t\n:upper()", "local s = t\n  :upper()"},
		{"return (\na &&\nb\n)", "return (\n  a &&\n  b\n)"},
		{"local s = [[\n  keep\n    me\n]]", "local s = [[\n  keep\n    me\n]]"},

		// blank lines
		{"a()\n\n\n\nb()", "a()\n\nb()"},
		{"\n\nif x {\n\na()\n\n}\n\n", "if x {\n  a()\n}"},

		// trailing commas
		{"local t = {\n1,\n2\n}", "local t = {\n  1,\n  2,\n}"},
		{"local t = {\n1 -- one\n}", "local t = {\n  1, -- one\n}"},
		{"local t = { 1, 2, }", "local t = { 1, 2 }"},
		{"for k, v in ... {\nprint(k)\n}", "for k, v in ... {\n  print(k)\n}"},

		// multiline braces get their contents on their own lines
		{"if x { error('y')\n}", "if x {\n  error('y')\n}"},
		{"if x {\nf() }", "if x {\n  f()\n}"},
		{"if x { a()\n} else { b() }", "if x {\n  a()\n} else { b() }"},
		{"local t = { a = 1,\nb = 2 }", "local t = {\n  a = 1,\n  b = 2,\n}"},
		{"f({ a = 1,\nb = 2 })", "f({\n  a = 1,\n  b = 2,\n})"},
		{"if x { -- why\na()\n}", "if x { -- why\n  a()\n}"},
		{"f(a,\nb)", "f(a,\n  b)"}, // parens are left alone

		// one line blocks stay on one line
		{"if x {y()}\nlocal f = () -> {return 1}", "if x { y() }\nlocal f = () -> { return 1 }"},
	}

	for _, c := range cases {
		got := testFormat(t, c.in, []byte(c.in), Options{})
		if got != nil && string(got) != c.want+"\n" {
			t.Errorf("%q\n  got  %q\n  want %q", c.in, got, c.want+"\n")
		}
	}
}

func TestFormatMatch(t *testing.T) {
	in := "match x {\n'a'|'b'=>{print(1)}\n_ if x:match('c')=>{\nprint(2)\n}\n}\nmatch(y)"
	want := "match x {\n  'a' | 'b' => { print(1) }\n  _ if x:match('c') => {\n    print(2)\n  }\n}\nmatch(y)\n"
	got := testFormat(t, "match", []byte(in), Options{Midnight: parse.MidnightOptions{Match: true}})
	if got != nil && string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestFormatErrors(t *testing.T) {
	if _, err := Format([]byte("local = 1"), Options{}); err == nil {
		t.Error("formatted invalid source")
	}
}
