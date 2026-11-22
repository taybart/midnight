package mngen

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvert(t *testing.T) {
	tests := []struct{ name, lua, midnight string }{
		{"operators",
			"local a = not x and y or z ~= 1",
			"local a = !x && y || z != 1"},
		{"blocks", `
if a then
  b()
elseif c then
  d()
else
  e()
end
while x do x = f() end
repeat local z = 1 until z == 1
do local y = 2 end`, `
if a {
  b()
} elseif c {
  d()
} else {
  e()
}
while x { x = f() }
repeat { local z = 1 } until z == 1
do { local y = 2 }`},
		{"loops", `
for i = 1, 10, 2 do print(i) end
for k, v in pairs(t) do print(k, v) end`, `
for i = 1, 10, 2 { print(i) }
for k, v in pairs(t) { print(k, v) }`},
		{"functions", `
local function f(a, ...) return a end
local g = function() end
function M.h(x) end
function M:m() return self end
call(function(x) return x * 2 end)`, `
local function f(a, ...) { return a }
local function g() {}
function M.h(x) {}
function M:m() { return self }
call((x) -> { return x * 2 })`},
		{"strings",
			`local s = "a{b}" .. 'c"d' .. "it's {x}" .. "\n\0"`,
			`local s = 'a{b}' .. 'c"d' .. "it's \{x}" .. '\n\000'`},
		{"block strings", `
local s = [[
a {b} \}
c]]
local t = [==[x]]
]==]`, `
local s = [[
a \{b} \\}
c]]
local t = [=[x]]
]=]`},
		{"numbers",
			"local n = 5. + 5.e2 + .5 + 0x1F",
			"local n = 5.0 + 5.0e2 + .5 + 0x1F"},
		{"keyword keys",
			"t.continue = { global = 1, module = 2 }",
			"t['continue'] = { ['global'] = 1, ['module'] = 2 }"},
		{"header braces", `
if x == {} then end
for k in next, {} do end`, `
if (x == {}) {}
for k in next, ({}) {}`},
		{"return list starting w/ a parenthesis",
			"return (f()), function() end",
			"return ((f()), () -> {})"},
		{"statements on a line",
			"a = b; (f)() catch = 1",
			"a = b; f(); catch = 1"},
		{"const index",
			"const[1] = 2",
			"(const)[1] = 2"},
		{"comments and layout", `
#!/usr/bin/env lua
-- header

local t = { -- trailing
  a = 1, --[[ block ]]
  -- own line
  ["b-c"] = f(
    1,
    2
  ),
}`, `
#!/usr/bin/env lua
-- header

local t = { -- trailing
  a = 1, --[[ block ]]
  -- own line
  ['b-c'] = f(
    1,
    2
  ),
}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Convert([]byte(strings.TrimPrefix(tt.lua, "\n")), "test.lua")
			if err != nil {
				t.Fatal(err)
			}
			if want := strings.TrimPrefix(tt.midnight, "\n") + "\n"; string(got) != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestConvertErrors(t *testing.T) {
	tests := []struct {
		name, lua string
		errors    []string
	}{
		{"keyword local", "local module = 1\nlocal x = continue", []string{
			"test.lua:1: local 'module' is a Midnight keyword (rename it)",
			"test.lua:2: global 'continue' is a Midnight keyword (rename it)",
		}},
		{"module()", `module("foo", package.seeall)`, []string{
			"test.lua:1: global 'module' is a Midnight keyword (Lua 5.1's module() isn't supported, return a table instead)",
		}},
		{"goto continue", "for i = 1, 2 do\n  goto continue\n  ::continue::\nend", []string{
			"test.lua:2: label 'continue' is a Midnight keyword (use Midnight's `continue` instead of `goto continue`)",
			"test.lua:3: label 'continue' is a Midnight keyword (use Midnight's `continue` instead of `goto continue`)",
		}},
		{"method", "obj:global()\nfunction M:module() end", []string{
			"test.lua:1: method name 'global' is a Midnight keyword (rename it)",
			"test.lua:2: method name 'module' is a Midnight keyword (rename it)",
		}},
		{"function name", "function M.continue() end", []string{
			"test.lua:1: function name 'continue' is a Midnight keyword (rename it)",
		}},
		{"parameter", "local f = function(global) end", []string{
			"test.lua:1: parameter 'global' is a Midnight keyword (rename it)",
		}},
		{"# line", "# not a shebang\nprint(1)", []string{
			"test.lua:1: a first line starting w/ '#' must be a '#!' line in Midnight",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Convert([]byte(tt.lua), "test.lua")
			var errs Errors
			if !errors.As(err, &errs) {
				t.Fatalf("expected Errors, got %v", err)
			}
			if got, want := err.Error(), strings.Join(tt.errors, "\n"); got != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

func TestConvertInvalidLua(t *testing.T) {
	_, err := Convert([]byte("local = 1"), "test.lua")
	var errs Errors
	if err == nil || errors.As(err, &errs) {
		t.Fatalf("expected a parse error, got %v", err)
	}
}

// TestConvertLuaFiles converts the repo's Lua (ex. the Lua test suites), which
// checks every result parses to the same program (see verify).
func TestConvertLuaFiles(t *testing.T) {
	expectedErrors := map[string]string{
		"_lua5.1-tests/attrib.lua": "local 'module' is a Midnight keyword",
		"_lua5.1-tests/main.lua":   "a first line starting w/ '#'",
	}

	count := 0
	for _, dir := range []string{"_glua-tests", "_lua5.1-tests", "lib", "testdata", "_tools"} {
		err := filepath.WalkDir(filepath.Join("..", dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".lua") {
				return err
			}
			name, _ := filepath.Rel("..", path)
			name = filepath.ToSlash(name)
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			count++

			_, err = Convert(source, name)
			switch want, ok := expectedErrors[name]; {
			case ok && (err == nil || !strings.Contains(err.Error(), want)):
				t.Errorf("%s: expected error %q, got %v", name, want, err)
			case !ok && err != nil:
				t.Errorf("%s: %v", name, err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count < 100 {
		t.Fatalf("only found %d Lua files", count)
	}
}
