package lua

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// Runs the Lua test scripts after a round trip through luagen
// (Lua -> AST -> Lua).
func TestLuagenScripts(t *testing.T) {
	for dir, tests := range map[string][]string{"_glua-tests": gluaTests, "_lua5.1-tests": luaTests} {
		for _, script := range tests {
			t.Run(filepath.Join(dir, script), func(t *testing.T) {
				testLuagenScript(t, dir, script)
			})
		}
	}
}

func testLuagenScript(t *testing.T, dir, script string) {
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir("..")
	// os.lua expects this to be unset (and sets it)
	defer os.Unsetenv("_____GLUATEST______")

	file, err := os.Open(script)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	chunk, err := parse.Parse(file, script)
	if err != nil {
		t.Fatal(err)
	}

	generated := luagen.Generate(chunk)

	L := NewState(Options{RegistrySize: 1024 * 20, CallStackSize: 1024})
	defer L.Close()
	L.SetMx(maxMemory)

	fn, err := L.Load(strings.NewReader(generated), script)
	if err != nil {
		t.Fatalf("%v\n%s", err, generated)
	}
	L.Push(fn)
	if err := L.PCall(0, MultRet, nil); err != nil {
		t.Error(err)
	}
}

// Runs the Midnight eval tests after compiling to Lua (Midnight -> AST -> Lua).
func TestLuagenMidnight(t *testing.T) {
	for _, c := range midnightEvalCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
			L.Close()
			continue
		}
		got := L.Get(-1)
		if got.Type() == LTTable && c.want == "table" {
			L.Close()
			continue
		}
		if got.String() != c.want {
			t.Errorf("%s\n%s\n  got %s, want %s", c.source, generated, got.String(), c.want)
		}
		L.Close()
	}
}

// The Midnight parser's temporary names (ex. a match subject) become
// __MIDNIGHT_TMP_N__ in generated Lua.
func TestLuagenTempNames(t *testing.T) {
	chunk, err := parse.ParseMidnight(strings.NewReader(`match x { 1 => { print(1) } }`), "<string>")
	if err != nil {
		t.Fatal(err)
	}
	src := luagen.Generate(chunk)
	if !strings.Contains(src, "__MIDNIGHT_TMP_") || strings.Contains(src, "__MIDNIGHT_MIDNIGHT_") {
		t.Errorf("unexpected temp names:\n%s", src)
	}
}
