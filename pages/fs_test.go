package pages_test

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/taybart/midnight/pages"
)

//go:embed testdata
var testdata embed.FS

// The testdata app, from an embed.FS: its require('counter') is found next to
// it, and its action works.
func TestFSEmbed(t *testing.T) {
	eng := pages.New(pages.Options{OnError: func(_ *http.Request, err error) { t.Error(err) }})
	defer eng.Close()
	ts := httptest.NewServer(eng.FS(testdata, "testdata/app.mn", pages.Props(func(*http.Request) (any, error) {
		return map[string]any{"name": "embed"}, nil
	})))
	defer ts.Close()

	_, html := get(t, ts.URL)
	if !strings.Contains(html, "<h1>hello embed</h1>") || !strings.Contains(html, "count 0") {
		t.Fatalf("render:\n%s", html)
	}
	state := stateOf(t, html)
	if _, body := post(t, ts.URL, state, `{"id":"`+actionID.FindStringSubmatch(html)[1]+`"}`); !strings.Contains(body, "count 1") {
		t.Errorf("action: %s", body)
	}
}

// countingFS counts opens of each file.
type countingFS struct {
	fs.FS
	opens map[string]*atomic.Int32
}

func (c countingFS) Open(name string) (fs.File, error) {
	if n, ok := c.opens[name]; ok {
		n.Add(1)
	}
	return c.FS.Open(name)
}

// require looks in the fs next to the entry (dots are directories, init
// files, .lua too), and nowhere else; files are compiled once.
func TestFSModules(t *testing.T) {
	fsys := countingFS{
		FS: fstest.MapFS{
			"ui/app.mn": {Data: []byte(`
local Header = require('header')
local nav = require('parts.nav')
local widgets = require('widgets')
local util = require('util')
local db = require('app.db')
local ok, err = pcall(require, 'testdata.counter') -- only on disk
return <main><Header/>{nav} {widgets} {util.shout('hi')} {db.name()} {tostring(ok)} {err}</main>`)},
			"ui/header.mn":         {Data: []byte(`return () -> <h1>header</h1>`)},
			"ui/parts/nav.mn":      {Data: []byte(`return 'nav'`)},
			"ui/widgets/init.mn":   {Data: []byte(`return 'widgets'`)},
			"ui/util.lua":          {Data: []byte(`return { shout = function(s) return s:upper() end }`)},
			"ui/unused/nothing.mn": {Data: []byte(`error('never loaded')`)},
		},
		opens: map[string]*atomic.Int32{"ui/app.mn": {}, "ui/header.mn": {}},
	}
	eng := pages.New(pages.Options{OnError: func(_ *http.Request, err error) { t.Error(err) }})
	defer eng.Close()
	ts := httptest.NewServer(eng.FS(fsys, "ui/app.mn", pages.Funcs("app.db", map[string]any{
		"name": func() string { return "db" },
	})))
	defer ts.Close()

	opens := map[string]int32{}
	for i := range 3 {
		_, html := get(t, ts.URL)
		for _, want := range []string{">header</h1>", "nav widgets HI db false", "no file &#39;ui/testdata/counter.mn&#39; in fs"} {
			if !strings.Contains(html, want) {
				t.Fatalf("missing %q in\n%s", want, html)
			}
		}
		// found and compiled once: later page loads (each a new state)
		// don't touch the fs
		for name, n := range fsys.opens {
			if i == 0 {
				opens[name] = n.Load()
			} else if n.Load() != opens[name] {
				t.Errorf("load %d: %s opened again (%d times, %d after the first load)", i+1, name, n.Load(), opens[name])
			}
		}
	}
}

// A bad entry path panics when the handler's made.
func TestFSBadPath(t *testing.T) {
	eng := pages.New(pages.Options{})
	defer eng.Close()
	fsys := fstest.MapFS{"ui/app.mn": {Data: []byte(`return <p/>`)}}
	for name, want := range map[string]string{
		"ui/nope.mn":  "pages: FS: open ui/nope.mn: file does not exist",
		"./ui/app.mn": `invalid path "./ui/app.mn"`,
		"/ui/app.mn":  `invalid path "/ui/app.mn"`,
		"ui":          "ui is a directory",
	} {
		func() {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), want) {
					t.Errorf("%s: got %v, want %q", name, r, want)
				}
			}()
			eng.FS(fsys, name)
		}()
	}
}
