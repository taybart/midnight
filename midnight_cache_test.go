package lua

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/taybart/midnight/parse"
)

func TestCompiledFileCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mod.mn")
	write := func(src string, mod time.Time) {
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	load := func(opts parse.MidnightOptions) (*LState, error) {
		L := NewState(Options{Midnight: opts})
		L.SetField(L.GetField(L.Get(GlobalsIndex), "package"), "path", LString(filepath.Join(dir, "?.mn")))
		return L, L.DoString(`m = require('mod')`)
	}

	markup := parse.MidnightOptions{}
	write("local t = { n = 1, el = tostring(<b/>) }\nreturn t", time.Unix(1000, 0))

	a, err := load(markup)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := load(markup)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	// shared code, separate modules
	key, proto, _ := a.compiledFile(path)
	if proto == nil {
		t.Fatalf("not cached: %+v", key)
	}
	if err := a.DoString(`m.n = 2`); err != nil {
		t.Fatal(err)
	}
	if err := b.DoString(`assert(m.n == 1 and m.el == '<b></b>', 'module shared between states')`); err != nil {
		t.Fatal(err)
	}

	// the midnight options are part of the key (w/ a missing factory, it fails)
	if c, err := load(parse.MidnightOptions{MarkupFactory: "missing.h"}); err == nil {
		c.Close()
		t.Error("used code compiled w/ other midnight options")
	}

	// a changed file is compiled again
	write("return { n = 3 }", time.Unix(2000, 0))
	d, err := load(markup)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.DoString(`assert(m.n == 3, 'stale module')`); err != nil {
		t.Fatal(err)
	}

	// and it can be turned off
	CacheCompiledFiles = false
	defer func() { CacheCompiledFiles = true }()
	if _, proto, _ := d.compiledFile(path); proto != nil {
		t.Error("cache used while off")
	}
}
