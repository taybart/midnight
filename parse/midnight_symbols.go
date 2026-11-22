package parse

import "github.com/taybart/midnight/ast"

// Symbols collects the names a Midnight file declares, and the names that refer
// to them, as the parser resolves them (for tooling, ex. go to definition in
// the language server). Set MidnightOptions.Symbols to collect them. Positions
// are byte offsets into the source.
type Symbols struct {
	Decls  []SymbolDecl
	Refs   []SymbolRef
	Fields []SymbolField
}

// SymbolDecl is a declared name: a local (including parameters and loop
// variables), a `global`, or a `module` name.
type SymbolDecl struct {
	Name       string
	Scope      string // "local", "global" or "module"
	Line       int
	Start, End int
	// Require is the module name when the declaration's value is
	// `require('name')` (ex. `const m = require('m')`), and Key is the field
	// when it's destructured from one (ex. `b` in `const { a: b } = ...` is
	// field a).
	Require, Key string
}

// SymbolField is a field accessed on a name, ex. `bar` in `m.bar` (only the
// first field after the name).
type SymbolField struct {
	Name       string
	Line       int
	Start, End int
	Ref        int // index of the name's reference in Refs
}

// SymbolRef is a use of a name (also the name in an assignment, or a bare
// `function x()`). Decl is the index of its declaration in Decls, or -1 when
// the file doesn't declare it (ex. a builtin, or a global set elsewhere).
type SymbolRef struct {
	Name       string
	Line       int
	Start, End int
	Decl       int
}

// DeclAt returns the declaration whose name, or a reference to it, contains
// offset, or nil.
func (s *Symbols) DeclAt(offset int) *SymbolDecl {
	for i := range s.Decls {
		if d := &s.Decls[i]; d.Start <= offset && offset < d.End {
			return d
		}
	}
	for _, r := range s.Refs {
		if r.Start <= offset && offset < r.End {
			if r.Decl < 0 {
				return nil
			}
			return &s.Decls[r.Decl]
		}
	}
	return nil
}

// FieldAt returns the field containing offset, or nil.
func (s *Symbols) FieldAt(offset int) *SymbolField {
	for i := range s.Fields {
		if f := &s.Fields[i]; f.Start <= offset && offset < f.End {
			return f
		}
	}
	return nil
}

// RefAt returns the reference containing offset, or nil.
func (s *Symbols) RefAt(offset int) *SymbolRef {
	for i := range s.Refs {
		if r := &s.Refs[i]; r.Start <= offset && offset < r.End {
			return r
		}
	}
	return nil
}

// Module returns the top level declaration of a `module` (exported) name, or
// nil.
func (s *Symbols) Module(name string) *SymbolDecl {
	return s.find(name, midnightScopeModule)
}

// Global returns the first declaration of `global name`, or nil.
func (s *Symbols) Global(name string) *SymbolDecl {
	return s.find(name, midnightScopeGlobal)
}

func (s *Symbols) find(name, scope string) *SymbolDecl {
	for i := range s.Decls {
		if d := &s.Decls[i]; d.Name == name && d.Scope == scope {
			return d
		}
	}
	return nil
}

/* Recording (no-ops unless p.syms is set) {{{ */

// tokenSpan returns the byte offsets of the word token at index.
func (p *midnightParser) tokenSpan(index int) (int, int) {
	t := p.tokens[index]
	return t.End - len(t.Value), t.End
}

// declareAt declares name, whose name token is at index.
func (p *midnightParser) declareAt(name, scope string, index int) {
	p.declare(name, scope)
	if p.syms == nil {
		return
	}
	start, end := p.tokenSpan(index)
	p.syms.Decls = append(p.syms.Decls, SymbolDecl{
		Name: name, Scope: scope, Line: p.tokens[index].Line, Start: start, End: end,
	})
	p.declIDs[len(p.declIDs)-1][name] = len(p.syms.Decls) - 1
}

// ref records a use of name, at the token at index. Only the first n bytes of
// the token are the name when n > 0 (ex. `ui` in the markup tag `ui.Card`).
func (p *midnightParser) ref(name string, index, n int) {
	if p.syms == nil {
		return
	}
	start, end := p.tokenSpan(index)
	if n > 0 {
		end = start + n
	}
	// (a name in scope w/o an id is a hoisted module name, which linkSymbols
	// links, or one held by holdDecls, so keep looking outward)
	decl := -1
	for i := len(p.scopes) - 1; i >= 0; i-- {
		if _, ok := p.scopes[i][name]; ok {
			if id, ok := p.declIDs[i][name]; ok {
				decl = id
				break
			}
		}
	}
	p.syms.Refs = append(p.syms.Refs, SymbolRef{
		Name: name, Line: p.tokens[index].Line, Start: start, End: end, Decl: decl,
	})
}

// linkSymbols resolves the references that the scopes couldn't: module names
// (hoisted, so they can be used before their declaration), and globals
// declared w/ `global` in some other scope.
func (p *midnightParser) linkSymbols() {
	if p.syms == nil {
		return
	}
	index := func(d *SymbolDecl) int {
		for i := range p.syms.Decls {
			if &p.syms.Decls[i] == d {
				return i
			}
		}
		return -1
	}
	for i := range p.syms.Refs {
		r := &p.syms.Refs[i]
		if r.Decl >= 0 {
			continue
		}
		if d := p.syms.Module(r.Name); d != nil {
			r.Decl = index(d)
		} else if d := p.syms.Global(r.Name); d != nil {
			r.Decl = index(d)
		}
	}
}

// holdDecls hides names, just declared in the current scope, from ref until
// the returned function is called.
func (p *midnightParser) holdDecls(names []string) func() {
	if p.syms == nil {
		return func() {}
	}
	top := p.declIDs[len(p.declIDs)-1]
	held := map[string]int{}
	for _, n := range names {
		if id, ok := top[n]; ok {
			held[n] = id
			delete(top, n)
		}
	}
	return func() {
		for n, id := range held {
			top[n] = id
		}
	}
}

// field records the name at index, after `.`, when what's before the `.` is
// the last name referenced (ex. `bar` in `m.bar`, but not in `m().bar`).
func (p *midnightParser) field(index int) {
	if p.syms == nil || len(p.syms.Refs) == 0 || p.tokens[index].Type != midnightWord {
		return
	}
	last := len(p.syms.Refs) - 1
	if p.syms.Refs[last].End != p.tokens[index-2].End {
		return
	}
	start, end := p.tokenSpan(index)
	p.syms.Fields = append(p.syms.Fields, SymbolField{
		Name: p.tokens[index].Value, Line: p.tokens[index].Line, Start: start, End: end, Ref: last,
	})
}

// requireName returns the module in `require('name')`, else "".
func requireName(expr ast.Expr) string {
	call, ok := expr.(*ast.FuncCallExpr)
	if !ok || len(call.Args) != 1 {
		return ""
	}
	if fn, ok := call.Func.(*ast.IdentExpr); !ok || fn.Value != "require" {
		return ""
	}
	if s, ok := call.Args[0].(*ast.StringExpr); ok {
		return s.Value
	}
	return ""
}

// markRequires sets Require (and Key) on the declarations from first on, made
// by a declaration of names (and destructures) w/ values exprs.
func (p *midnightParser) markRequires(first int, names []string, destructures []*midnightDestructure, exprs []ast.Expr) {
	if p.syms == nil {
		return
	}
	decls := p.syms.Decls[first:]
	if len(destructures) == 0 {
		for i := range decls {
			if i < len(exprs) {
				decls[i].Require = requireName(exprs[i])
			}
		}
		return
	}
	if len(names) != 1 || len(exprs) != 1 || destructures[0].keys == nil {
		return
	}
	if mod := requireName(exprs[0]); mod != "" {
		for i := range decls {
			decls[i].Require, decls[i].Key = mod, destructures[0].keys[i]
		}
	}
}

// refModule records `x` in `module.x`, which always refers to the module's x
// (linked by linkSymbols, even when a local shadows it).
func (p *midnightParser) refModule(name string, index int) {
	start, end := p.tokenSpan(index)
	p.syms.Refs = append(p.syms.Refs, SymbolRef{
		Name: name, Line: p.tokens[index].Line, Start: start, End: end, Decl: -1,
	})
}

/* }}} */
