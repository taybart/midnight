// Package lint checks chunks (from either the Midnight or Lua parser) for likely
// mistakes: undefined / undeclared globals, unused locals, shadowing, and
// unreachable code.
//
// It works on the AST, so it sees Midnight after lowering. Names made up by the
// Midnight parser (ex. destructuring / match temporaries) are never reported, and
// `global x` / `module x` names don't need to be declared (they compile to
// `_G.x` / `_MODULE.x`).
package lint

import (
	"sort"
	"strconv"
	"strings"

	"github.com/taybart/midnight/ast"
)

const (
	RuleUndefinedGlobal = "undefined-global"
	RuleSetGlobal       = "set-global"
	RuleUnusedLocal     = "unused-local"
	RuleShadow          = "shadow"
	RuleRedefined       = "redefined"
	RuleUnreachable     = "unreachable"
	// a `=>` function that builds markup elements and doesn't use self:
	// probably a component, whose props would be `self`
	RuleFatArrowComponent = "fat-arrow-component"
)

type Diagnostic struct {
	Line    int
	Rule    string
	Message string
}

type Options struct {
	// Additional globals that may be read and set (ex. ones provided by the
	// host program).
	Globals []string
}

// StandardGlobals are the globals defined by Lua (5.1 - 5.5), LuaJIT, and
// gopher-lua / midnight (ex. `bit32`, `channel`).
var StandardGlobals = []string{
	"_G", "_VERSION", "_ENV", "arg", "assert", "bit32", "channel",
	"collectgarbage", "coroutine", "debug", "dofile", "error", "gcinfo",
	"getfenv", "getmetatable", "io", "ipairs", "jit", "load", "loadfile",
	"loadstring", "math", "module", "newproxy", "next", "os", "package",
	"pairs", "pcall", "print", "rawequal", "rawget", "rawlen", "rawset",
	"require", "select", "setfenv", "setmetatable", "string", "table",
	"tonumber", "tostring", "type", "unpack", "utf8", "warn", "xpcall",
}

type varKind int

const (
	kindLocal varKind = iota
	kindParam
	kindLoop
)

type variable struct {
	name string
	line int
	kind varKind
	used bool
}

type scope struct {
	vars map[string]*variable
	// Declaration order, so unused locals are reported deterministically.
	order []*variable
}

type globalRef struct {
	name string
	line int
}

type checker struct {
	scopes      []*scope
	globals     map[string]bool
	globalSets  map[string]bool
	globalReads []globalRef
	diagnostics []Diagnostic
	// functions being checked, innermost last
	functions []*function
}

type function struct {
	hasElement bool
}

// Check lints a chunk. Diagnostics are sorted by line.
func Check(chunk []ast.Stmt, opts Options) []Diagnostic {
	c := &checker{globals: map[string]bool{}, globalSets: map[string]bool{}}
	for _, name := range StandardGlobals {
		c.globals[name] = true
	}
	for _, name := range opts.Globals {
		c.globals[name] = true
	}

	c.pushScope()
	c.block(chunk)
	c.popScope()

	// Reads of globals set somewhere in the chunk are only reported at the set.
	for _, ref := range c.globalReads {
		if !c.globalSets[ref.name] {
			c.report(ref.line, RuleUndefinedGlobal, "undefined variable '"+ref.name+"'")
		}
	}

	sort.SliceStable(c.diagnostics, func(i, j int) bool { return c.diagnostics[i].Line < c.diagnostics[j].Line })
	return c.diagnostics
}

func (c *checker) report(line int, rule, message string) {
	c.diagnostics = append(c.diagnostics, Diagnostic{Line: line, Rule: rule, Message: message})
}

// isInternal checks for names made up by the Midnight parser (not valid
// identifiers, ex. `(midnight 1)`).
func isInternal(name string) bool { return strings.HasPrefix(name, "(") }

// isIgnored checks for names that are intentionally unused (ex. `_`, `_x`).
func isIgnored(name string) bool { return strings.HasPrefix(name, "_") }

/* Scopes {{{ */

func (c *checker) pushScope() { c.scopes = append(c.scopes, &scope{vars: map[string]*variable{}}) }

func (c *checker) popScope() {
	s := c.scopes[len(c.scopes)-1]
	c.scopes = c.scopes[:len(c.scopes)-1]

	for _, v := range s.order {
		if v.kind == kindLocal && !v.used && !isIgnored(v.name) && !isInternal(v.name) {
			c.report(v.line, RuleUnusedLocal, "unused local '"+v.name+"'")
		}
	}
}

func (c *checker) lookup(name string) *variable {
	for i := len(c.scopes) - 1; i >= 0; i-- {
		if v, ok := c.scopes[i].vars[name]; ok {
			return v
		}
	}
	return nil
}

func (c *checker) declare(name string, line int, kind varKind) {
	if !isInternal(name) && !isIgnored(name) && name != "self" {
		current := c.scopes[len(c.scopes)-1]
		if prev, ok := current.vars[name]; ok {
			c.report(line, RuleRedefined, "redefinition of local '"+name+"' (from line "+strconv.Itoa(prev.line)+")")
		} else if prev := c.lookup(name); prev != nil {
			c.report(line, RuleShadow, "local '"+name+"' shadows local from line "+strconv.Itoa(prev.line))
		}
	}

	v := &variable{name: name, line: line, kind: kind}
	current := c.scopes[len(c.scopes)-1]
	current.vars[name] = v
	current.order = append(current.order, v)
}

/* }}} */

/* Statements {{{ */

func (c *checker) block(stmts []ast.Stmt) {
	terminated := false
	for _, stmt := range stmts {
		if terminated {
			// Labels aren't code (ex. the label `continue` jumps to).
			if _, ok := stmt.(*ast.LabelStmt); !ok {
				c.report(stmt.Line(), RuleUnreachable, "unreachable code")
				terminated = false // only report the first statement
			}
		}

		c.stmt(stmt)

		switch stmt.(type) {
		case *ast.ReturnStmt, *ast.BreakStmt, *ast.GotoStmt:
			terminated = true
		}
	}
}

func (c *checker) scopedBlock(stmts []ast.Stmt) {
	c.pushScope()
	c.block(stmts)
	c.popScope()
}

func (c *checker) stmt(stmt ast.Stmt) {
	switch st := stmt.(type) {
	case *ast.AssignStmt:
		c.exprs(st.Rhs)
		for _, lhs := range st.Lhs {
			c.assignTarget(lhs)
		}
	case *ast.LocalAssignStmt:
		// `local function f` (and `local f = function`, which the compiler
		// treats the same) can refer to itself.
		if len(st.Names) == 1 && len(st.Exprs) == 1 {
			if fn, ok := st.Exprs[0].(*ast.FunctionExpr); ok {
				c.declare(st.Names[0], st.Line(), kindLocal)
				c.function(fn, false)
				return
			}
		}
		c.exprs(st.Exprs)
		for _, name := range st.Names {
			c.declare(name, st.Line(), kindLocal)
		}
	case *ast.FuncCallStmt:
		c.expr(st.Expr)
	case *ast.DoBlockStmt:
		c.scopedBlock(st.Stmts)
	case *ast.WhileStmt:
		c.expr(st.Condition)
		c.scopedBlock(st.Stmts)
	case *ast.RepeatStmt:
		// The condition can see the body's locals.
		c.pushScope()
		c.block(st.Stmts)
		c.expr(st.Condition)
		c.popScope()
	case *ast.IfStmt:
		c.expr(st.Condition)
		c.scopedBlock(st.Then)
		c.scopedBlock(st.Else)
	case *ast.NumberForStmt:
		c.expr(st.Init)
		c.expr(st.Limit)
		if st.Step != nil {
			c.expr(st.Step)
		}
		c.pushScope()
		c.declare(st.Name, st.Line(), kindLoop)
		c.scopedBlock(st.Stmts)
		c.popScope()
	case *ast.GenericForStmt:
		c.exprs(st.Exprs)
		c.pushScope()
		for _, name := range st.Names {
			c.declare(name, st.Line(), kindLoop)
		}
		c.scopedBlock(st.Stmts)
		c.popScope()
	case *ast.FuncDefStmt:
		if st.Name.Receiver != nil {
			c.expr(st.Name.Receiver)
		} else {
			c.assignTarget(st.Name.Func)
		}
		c.function(st.Func, st.Name.Method != "")
	case *ast.ReturnStmt:
		c.exprs(st.Exprs)
	}
}

// assignTarget handles the left hand side of an assignment, which sets (not
// uses) a local.
func (c *checker) assignTarget(expr ast.Expr) {
	ident, ok := expr.(*ast.IdentExpr)
	if !ok {
		c.expr(expr)
		return
	}
	if c.lookup(ident.Value) != nil || c.globals[ident.Value] {
		return
	}
	c.globalSets[ident.Value] = true
	c.report(ident.Line(), RuleSetGlobal, "setting undeclared global '"+ident.Value+"' (declare it w/ 'global', or use 'local')")
}

/* }}} */

/* Expressions {{{ */

func (c *checker) exprs(exprs []ast.Expr) {
	for _, expr := range exprs {
		c.expr(expr)
	}
}

func (c *checker) expr(expr ast.Expr) {
	switch ex := expr.(type) {
	case *ast.IdentExpr:
		if v := c.lookup(ex.Value); v != nil {
			v.used = true
		} else if !c.globals[ex.Value] {
			c.globalReads = append(c.globalReads, globalRef{ex.Value, ex.Line()})
		}
	case *ast.AttrGetExpr:
		c.expr(ex.Object)
		c.expr(ex.Key)
	case *ast.TableExpr:
		for _, field := range ex.Fields {
			if field.Key != nil {
				c.expr(field.Key)
			}
			c.expr(field.Value)
		}
	case *ast.FuncCallExpr:
		if ex.Element && len(c.functions) > 0 {
			c.functions[len(c.functions)-1].hasElement = true
		}
		if ex.Func != nil {
			c.expr(ex.Func)
		}
		if ex.Receiver != nil {
			c.expr(ex.Receiver)
		}
		c.exprs(ex.Args)
	case *ast.LogicalOpExpr:
		c.expr(ex.Lhs)
		c.expr(ex.Rhs)
	case *ast.RelationalOpExpr:
		c.expr(ex.Lhs)
		c.expr(ex.Rhs)
	case *ast.StringConcatOpExpr:
		c.expr(ex.Lhs)
		c.expr(ex.Rhs)
	case *ast.ArithmeticOpExpr:
		c.expr(ex.Lhs)
		c.expr(ex.Rhs)
	case *ast.UnaryMinusOpExpr:
		c.expr(ex.Expr)
	case *ast.UnaryNotOpExpr:
		c.expr(ex.Expr)
	case *ast.UnaryLenOpExpr:
		c.expr(ex.Expr)
	case *ast.FunctionExpr:
		c.function(ex, false)
	}
}

func (c *checker) function(fn *ast.FunctionExpr, isMethod bool) {
	c.pushScope()
	if isMethod {
		c.declare("self", fn.Line(), kindParam)
	}
	for _, name := range fn.ParList.Names {
		c.declare(name, fn.Line(), kindParam)
	}
	f := &function{}
	c.functions = append(c.functions, f)
	c.scopedBlock(fn.Stmts)
	c.functions = c.functions[:len(c.functions)-1]

	if self := c.scopes[len(c.scopes)-1].vars["self"]; fn.FatArrow && f.hasElement && self != nil && !self.used {
		c.report(fn.Line(), RuleFatArrowComponent, "'=>' adds 'self' as the first parameter, so a component's props would be 'self' (use '->')")
	}
	c.popScope()
}

/* }}} */
