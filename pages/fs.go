package pages

import (
	"bytes"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"sync"

	lua "github.com/taybart/midnight"
	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/parse"
)

// FS serves the page at name in fsys (ex. an embed.FS): a midnight (or lua)
// file that returns a component, a markup element, or a response table.
//
//	//go:embed ui
//	var ui embed.FS
//
//	mux.Handle("/app/", http.StripPrefix("/app", eng.FS(ui, "ui/app.mn")))
//
// `require` finds the page's modules in fsys, next to it (ex.
// require('header') is ui/header.mn, require('parts.nav') is
// ui/parts/nav.mn), and nowhere else: not on disk, and not in the engine's
// Path. Modules from LuaFuncs / Funcs and the built-in ones still work.
//
// Files are compiled once (on first use) and kept, so changes to fsys aren't
// seen: it's for files that don't change, like an embed.FS. Use File for
// files that do.
//
// It panics if name isn't a file in fsys, so a typo fails at startup.
func (e *Engine) FS(fsys fs.FS, name string, opts ...Option) http.Handler {
	if !fs.ValidPath(name) {
		panic(fmt.Sprintf("pages: FS: invalid path %q (paths in an fs.FS are like \"ui/app.mn\": unrooted, with slashes)", name))
	}
	if info, err := fs.Stat(fsys, name); err != nil {
		panic("pages: FS: " + err.Error())
	} else if info.IsDir() {
		panic(fmt.Sprintf("pages: FS: %s is a directory", name))
	}
	src := &fsSource{fsys: fsys, dir: path.Dir(name)}
	h := &handler{
		engine: e,
		fs:     src,
		load: func(l *lua.LState) error {
			proto, err := src.compile(l, name)
			if err != nil {
				return err
			}
			l.Push(l.NewFunctionFromProto(proto))
			return l.PCall(0, 1, nil)
		},
	}
	return h.with(opts)
}

// fsSource loads a page's files from an fs.FS.
type fsSource struct {
	fsys   fs.FS
	dir    string   // the entry's directory, where modules are looked for
	protos sync.Map // path -> *lua.FunctionProto (compiled code is immutable)
	found  sync.Map // module -> foundModule (the fs doesn't change)
}

type foundModule struct {
	path  string   // "" if not found
	tried []string // where it looked, if not found
}

// moduleExts are what a module name can be, in order (like package.path's
// "?.mn;?.midnight;?.lua", then "?/init.*").
var moduleExts = append(append([]string{}, parse.Extensions...), ".lua")

// compile returns the compiled file at name (cached).
func (s *fsSource) compile(l *lua.LState, name string) (*lua.FunctionProto, error) {
	if p, ok := s.protos.Load(name); ok {
		return p.(*lua.FunctionProto), nil
	}
	src, err := fs.ReadFile(s.fsys, name)
	if err != nil {
		return nil, err
	}
	var chunk []ast.Stmt
	if parse.IsMidnightPath(name) {
		chunk, err = parse.ParseMidnightWithOptions(bytes.NewReader(src), name, l.Options.Midnight)
	} else {
		chunk, err = parse.Parse(bytes.NewReader(src), name)
	}
	if err != nil {
		return nil, err
	}
	proto, err := lua.Compile(chunk, name)
	if err != nil {
		return nil, err
	}
	actual, _ := s.protos.LoadOrStore(name, proto)
	return actual.(*lua.FunctionProto), nil
}

// find returns the file of a module (ex. "parts.nav" is parts/nav.mn), or
// the paths it tried (cached).
func (s *fsSource) find(module string) (string, []string) {
	if f, ok := s.found.Load(module); ok {
		return f.(foundModule).path, f.(foundModule).tried
	}
	p, tried := s.search(module)
	s.found.Store(module, foundModule{path: p, tried: tried})
	return p, tried
}

func (s *fsSource) search(module string) (string, []string) {
	base := path.Join(s.dir, strings.ReplaceAll(module, ".", "/"))
	var tried []string
	for _, stem := range []string{base, path.Join(base, "init")} {
		for _, ext := range moduleExts {
			p := stem + ext
			if info, err := fs.Stat(s.fsys, p); err == nil && !info.IsDir() {
				return p, nil
			}
			tried = append(tried, p)
		}
	}
	return "", tried
}

// loader is a package.loaders entry: it returns the module's chunk, or a
// message of where it looked.
func (s *fsSource) loader(l *lua.LState) int {
	module := l.CheckString(1)
	name, tried := s.find(module)
	if name == "" {
		l.Push(lua.LString("\n\tno file '" + strings.Join(tried, "' in fs\n\tno file '") + "' in fs"))
		return 1
	}
	proto, err := s.compile(l, name)
	if err != nil {
		l.RaiseError("error loading module '%s' from %s:\n\t%s", module, name, err.Error())
	}
	l.Push(l.NewFunctionFromProto(proto))
	return 1
}

// install makes require look in the fs (after package.preload, which has the
// built-in modules and LuaFuncs / Funcs), instead of on disk.
func (s *fsSource) install(l *lua.LState) {
	pkg := l.GetGlobal("package")
	loaders, ok := l.GetField(pkg, "loaders").(*lua.LTable)
	if !ok {
		return
	}
	preload := loaders.RawGetInt(1)
	fresh := l.CreateTable(2, 0)
	fresh.RawSetInt(1, preload)
	fresh.RawSetInt(2, l.NewFunction(s.loader))
	l.SetField(pkg, "loaders", fresh)
	l.SetField(l.Get(lua.RegistryIndex), "_LOADERS", fresh)
}
