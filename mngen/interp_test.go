package mngen

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConvertInterp(t *testing.T) {
	tests := []struct{ name, lua, midnight string }{
		{"values", `local s = 'id: ' .. id .. '!'`, `local s = "id: {id}!"`},
		{"leading value", `local s = a .. ' and ' .. b`, `local s = "{a} and {b}"`},
		{"tostring", `local s = 'n=' .. tostring(n)`, `local s = "n={n}"`},
		{"merged literals", `local s = 'a' .. 'b' .. x`, `local s = "ab{x}"`},
		{"escapes", `local s = 'say "' .. x .. '" {ok}\n'`, `local s = "say \"{x}\" \{ok}\n"`},
		{"calls and strings", `local s = 'h: ' .. f('x', "it's")`, `local s = "h: {f('x', "it's")}"`},
		{"method call", `local s = 'x' .. obj:name()`, `local s = "x{obj:name()}"`},
		{"arithmetic", `local s = 'n=' .. n + 1`, `local s = "n={n + 1}"`},
		{"parens kept", `local n = #('a' .. x)`, `local n = #("a{x}")`},
		{"method on result", `local s = ('a' .. x):upper()`, `local s = ("a{x}"):upper()`},
		{"no literal", `local s = a .. b`, `local s = a .. b`},
		{"only literals", `local s = 'a' .. 'b'`, `local s = 'a' .. 'b'`},
		{"empty literal", `local s = '' .. x`, `local s = '' .. x`},
		{"spans lines", "local s = 'a' ..\n  x", "local s = 'a' ..\n  x"},
		{"multiline operand", "local s = 'a' .. f(function()\n  return 1\nend)",
			"local s = 'a' .. f(() -> {\n  return 1\n})"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ConvertWith([]byte(tt.lua), "test.lua", Options{Interp: true})
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.midnight + "\n"; string(got) != want {
				t.Errorf("got:\n%s\nwant:\n%s", got, want)
			}
		})
	}
}

// TestConvertInterpLuaFiles converts the repo's Lua w/ Options.Interp, which
// checks every result still parses to the same program (modulo tostring, see
// interpOperands).
func TestConvertInterpLuaFiles(t *testing.T) {
	count := 0
	for _, dir := range []string{"_glua-tests", "_lua5.1-tests", "lib", "testdata", "_tools"} {
		err := filepath.WalkDir(filepath.Join("..", dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".lua") {
				return err
			}
			source, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			count++
			_, err = ConvertWith(source, path, Options{Interp: true})
			if err != nil && strings.Contains(err.Error(), "conversion bug") {
				t.Errorf("%s: %v", path, err)
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
