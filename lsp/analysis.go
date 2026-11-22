package lsp

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/lint"
	"github.com/taybart/midnight/parse"
)

// file is a parsed Midnight file (syms is empty, and chunk nil, for other
// files). err is the syntax error, if any (syms are collected up to it).
type file struct {
	path, text string
	syms       *parse.Symbols
	chunk      []ast.Stmt
	err        error
}

type cachedFile struct {
	modTime time.Time
	*file
}

// span is a range of bytes in a file. text is the file's text, when known.
type span struct {
	path, text string
	start, end int
}

// file returns path parsed: the open document, or the file on disk (cached by
// modification time). nil when it can't be read.
func (s *Server) file(path string) *file {
	if text, ok := s.docs[path]; ok {
		return s.parse(path, text)
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if c, ok := s.cache[path]; ok && c.modTime.Equal(info.ModTime()) {
		return c.file
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	f := s.parse(path, string(data))
	s.cache[path] = &cachedFile{modTime: info.ModTime(), file: f}
	return f
}

// parse collects text's symbols, up to the first syntax error (so a file
// that's being edited still mostly works).
func (s *Server) parse(path, text string) *file {
	f := &file{path: path, text: text, syms: &parse.Symbols{}}
	if strings.HasSuffix(path, ".lua") {
		return f
	}
	opts := s.opts
	opts.Symbols = f.syms
	f.chunk, f.err = parse.ParseMidnightWithOptions(strings.NewReader(text), path, opts)
	return f
}

func (f *file) declSpan(d *parse.SymbolDecl) span {
	return span{path: f.path, text: f.text, start: d.Start, end: d.End}
}

// declIndex returns the index of the declaration at offset (the declared name,
// or a reference to it), or -1.
func declIndex(syms *parse.Symbols, offset int) int {
	for i, d := range syms.Decls {
		if d.Start <= offset && offset < d.End {
			return i
		}
	}
	if r := syms.RefAt(offset); r != nil {
		return r.Decl
	}
	return -1
}

/* Definition {{{ */

// definition returns where the name (or field) at offset is defined.
func (s *Server) definition(f *file, offset int) []span {
	dir := filepath.Dir(f.path)

	// `m.bar`, where `m = require('m')`
	if field := f.syms.FieldAt(offset); field != nil {
		ref := f.syms.Refs[field.Ref]
		if ref.Decl >= 0 {
			if mod := f.syms.Decls[ref.Decl].Require; mod != "" {
				return s.moduleField(mod, field.Name, dir)
			}
		}
		return nil
	}

	// a declaration: from a require, go to the module (or its field)
	for i := range f.syms.Decls {
		d := &f.syms.Decls[i]
		if d.Start <= offset && offset < d.End {
			if d.Require != "" {
				if found := s.moduleField(d.Require, d.Key, dir); found != nil {
					return found
				}
			}
			return []span{f.declSpan(d)}
		}
	}

	// `require('m')` / `require('m').bar`, on the module name or the field
	if mod, key, ok := inlineRequire(f.text, offset); ok {
		return s.moduleField(mod, key, dir)
	}

	ref := f.syms.RefAt(offset)
	if ref == nil {
		return nil
	}
	if ref.Decl < 0 {
		return s.workspaceGlobal(ref.Name, f.path)
	}
	d := &f.syms.Decls[ref.Decl]
	// a destructured require (`const { render } = require('m')`) goes straight
	// to the module's field, since the declaration is just a rename
	if d.Key != "" {
		if found := s.moduleField(d.Require, d.Key, dir); found != nil {
			return found
		}
	}
	return []span{f.declSpan(d)}
}

// moduleField returns where module mod (required from dir) defines key, or the
// top of the module when key is "" or isn't found. nil if the module isn't
// found.
func (s *Server) moduleField(mod, key, dir string) []span {
	path := s.resolveModule(mod, dir)
	if path == "" {
		return nil
	}
	top := []span{{path: path}}
	if key == "" {
		return top
	}
	f := s.file(path)
	if f == nil {
		return top
	}
	if d := f.syms.Module(key); d != nil {
		return []span{f.declSpan(d)}
	}
	if start, end := findField(f.text, key); end > 0 {
		return []span{{path: path, text: f.text, start: start, end: end}}
	}
	return top
}

// Lua (and Midnight) module tables, ex. `function M.key(`, `M.key =`,
// `local function key(`, or `key = ` in a returned table. The key is group 1.
var fieldPatterns = []string{
	`(?m)^\s*function\s+[\w.]*[.:](KEY)\b`,
	`(?m)^\s*\w+\.(KEY)\s*=[^=]`,
	`(?m)^\s*(?:local\s+)?function\s+(KEY)\b`,
	`(?m)^\s*(KEY)\s*=[^=]`,
}

func findField(text, key string) (int, int) {
	for _, pattern := range fieldPatterns {
		re := regexp.MustCompile(strings.ReplaceAll(pattern, "KEY", regexp.QuoteMeta(key)))
		if m := re.FindStringSubmatchIndex(text); m != nil {
			return m[2], m[3]
		}
	}
	return 0, 0
}

var requirePattern = regexp.MustCompile(`require\s*\(?\s*['"]([\w./-]+)['"]\s*\)?(?:\s*[.:]\s*(\w+))?`)

// inlineRequire checks for offset on the module name in `require('m')`, or on
// bar in `require('m').bar`, returning the module and field ("" for the name).
func inlineRequire(text string, offset int) (mod, key string, ok bool) {
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	lineEnd := strings.IndexByte(text[offset:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text) - offset
	}
	line, col := text[lineStart:offset+lineEnd], offset-lineStart
	for _, m := range requirePattern.FindAllStringSubmatchIndex(line, -1) {
		switch {
		case m[2] <= col && col < m[3]:
			return line[m[2]:m[3]], "", true
		case m[4] >= 0 && m[4] <= col && col < m[5]:
			return line[m[2]:m[3]], line[m[4]:m[5]], true
		}
	}
	return "", "", false
}

// Module file patterns, like package.path w/ midnight's additions (see
// loadlib.go): Midnight sources before (possibly generated) Lua.
var modulePatterns = []string{"?.mn", "?.midnight", "?/init.mn", "?/init.midnight", "?.lua", "?/init.lua"}

// resolveModule finds the file `require(mod)` loads from dir: relative to dir
// and each parent up to the workspace root (or the filesystem root outside of
// it), also trying their midnight.json `compile.src` and a `lua/` dir (neovim
// plugins).
func (s *Server) resolveModule(mod, dir string) string {
	rel := filepath.FromSlash(strings.ReplaceAll(mod, ".", "/"))
	for {
		bases := []string{dir}
		if src := projectSrc(dir); src != "" {
			bases = append(bases, src)
		}
		bases = append(bases, filepath.Join(dir, "lua"))
		for _, base := range bases {
			for _, pattern := range modulePatterns {
				path := filepath.Join(base, strings.ReplaceAll(pattern, "?", rel))
				if info, err := os.Stat(path); err == nil && !info.IsDir() {
					return path
				}
			}
		}
		parent := filepath.Dir(dir)
		if dir == s.root || parent == dir {
			return ""
		}
		dir = parent
	}
}

// projectSrc returns the absolute `compile.src` of dir/midnight.json, or "".
func projectSrc(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "midnight.json"))
	if err != nil {
		return ""
	}
	v, err := lint.ParseJSONC(data)
	if err != nil {
		return ""
	}
	root, _ := v.(map[string]any)
	compile, _ := root["compile"].(map[string]any)
	src, _ := compile["src"].(string)
	if src == "" {
		return ""
	}
	return filepath.Join(dir, src)
}

// maxWorkspaceFiles caps the files searched for globals.
const maxWorkspaceFiles = 5000

// workspaceGlobal finds `global name` in the workspace's Midnight files (the
// open documents first, then from the root). from is searched first.
func (s *Server) workspaceGlobal(name, from string) []span {
	check := func(path string) []span {
		if f := s.file(path); f != nil {
			if d := f.syms.Global(name); d != nil {
				return []span{f.declSpan(d)}
			}
		}
		return nil
	}
	if found := check(from); found != nil {
		return found
	}
	for path := range s.docs {
		if found := check(path); found != nil {
			return found
		}
	}
	if s.root == "" {
		return nil
	}

	var found []span
	count := 0
	_ = filepath.WalkDir(s.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != s.root && (strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor") {
				return filepath.SkipDir
			}
			return nil
		}
		if !parse.IsMidnightPath(path) {
			return nil
		}
		if count++; count > maxWorkspaceFiles {
			return filepath.SkipAll
		}
		if found = check(path); found != nil {
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

/* }}} */

// references returns the declaration of the name at offset, then its
// references in the file (nil if the name isn't declared in the file).
func references(f *file, offset int) []span {
	decl := declIndex(f.syms, offset)
	if decl < 0 {
		return nil
	}
	spans := []span{f.declSpan(&f.syms.Decls[decl])}
	for _, r := range f.syms.Refs {
		if r.Decl == decl {
			spans = append(spans, span{path: f.path, text: f.text, start: r.Start, end: r.End})
		}
	}
	return spans
}
