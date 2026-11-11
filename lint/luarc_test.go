package lint

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/taybart/midnight/parse"
)

func TestDefinedGlobals(t *testing.T) {
	source := `---@meta
---@type harmony
harmony = {}
function sleep(secs) end
function harmony.send(msg) end
function harmony:method() end
local Users = {}
Users = {}
local function helper() end
a, b.c = 1, 2
do x = 1 end
harmony = {}
`
	chunk, err := parse.Parse(strings.NewReader(source), "<string>")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"harmony", "sleep", "a"}
	if got := DefinedGlobals(chunk); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLuarcGlobals(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("defs/a.lua", "harmony = {}\n")
	write("defs/nested/b.lua", "function fetch(url) end\n")
	write("defs/notes.txt", "ignored = {}\n")
	write("one.lua", "regex = {}\n")
	write("bots/.luarc.json", `{
		"workspace.library": ["../defs", "../one.lua", "../missing", "${3rd}/love2d"],
		"diagnostics.globals": ["host_thing"]
	}`)
	write("nested/.luarc.json", `{"workspace": {"library": ["../defs"]}}`)
	write("bots/game/deep/file.mn", "")

	path := FindLuarc(filepath.Join(root, "bots/game/deep"))
	if path != filepath.Join(root, "bots/.luarc.json") {
		t.Fatalf("FindLuarc: %q", path)
	}
	globals, err := LuarcGlobals(path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"host_thing", "harmony", "fetch", "regex"}
	if !reflect.DeepEqual(globals, want) {
		t.Errorf("got %v, want %v", globals, want)
	}

	globals, err = LuarcGlobals(filepath.Join(root, "nested/.luarc.json"))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"harmony", "fetch"}; !reflect.DeepEqual(globals, want) {
		t.Errorf("nested: got %v, want %v", globals, want)
	}

	// the globals are known to Check
	got := check(t, "harmony.send({})\nfetch('x')\nhost_thing()", Options{Globals: globals})
	if len(got) != 1 || got[0] != "3:undefined-global" {
		t.Errorf("check: %v", got)
	}
}

func TestParseJSONC(t *testing.T) {
	got, err := parseJSONC([]byte(`// lua-language-server allows comments
	{
		/* and block comments */
		"workspace.library": ["../defs", "a\"b\\c\u00e9\ud83d\ude00",], // trailing commas
		"n": -1.5e2, "t": true, "f": false, "z": null, "o": {}, "a": [],
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"workspace.library": []any{"../defs", "a\"b\\cé😀"},
		"n":                 -150.0, "t": true, "f": false, "z": nil, "o": map[string]any{}, "a": []any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v\nwant %#v", got, want)
	}

	for _, bad := range []string{``, `{`, `{"a" 1}`, `{"a": 1} x`, `[1 2]`, `{a: 1}`, `"\x"`, `"open`, `tru`} {
		if _, err := parseJSONC([]byte(bad)); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
