package mngen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/taybart/midnight/ast"
)

/* Statements {{{ */

// block writes `{ stmts }`, w/ the `}` on line `end`.
func (g *generator) block(stmts []ast.Stmt, end int) {
	g.write(" {")
	g.depth++
	g.stmts(stmts)
	g.depth--
	g.sync(end)
	g.keyword("}")
}

func (g *generator) stmts(stmts []ast.Stmt) {
	for i, stmt := range stmts {
		g.sync(stmt.Line())
		if i > 0 && g.stmtEnd == g.line {
			// ex. `a = b; (f)()`, which would be a call w/o the `;`
			g.write(";")
		}
		g.stmt(stmt)
		g.stmtEnd = g.line
	}
}

func (g *generator) stmt(stmt ast.Stmt) {
	g.space()

	// `const[k] = v` (w/ a global named const) would be a const declaration
	// (`const [a] = t`), `(const)[k] = v` is the same as in Lua.
	start := len(g.buf)
	defer func() {
		for start < len(g.buf) && g.buf[start] == ' ' {
			start++
		}
		if bytes.HasPrefix(g.buf[start:], []byte("const[")) {
			g.buf = append(g.buf[:start], append([]byte("(const)"), g.buf[start+len("const"):]...)...)
		}
	}()

	switch st := stmt.(type) {
	case *ast.AssignStmt:
		g.exprList(st.Lhs)
		g.write(" = ")
		g.exprList(st.Rhs)
	case *ast.LocalAssignStmt:
		g.localAssign(st)
	case *ast.FuncCallStmt:
		g.expr(st.Expr, 0)
	case *ast.DoBlockStmt:
		g.write("do")
		g.block(st.Stmts, st.LastLine())
	case *ast.WhileStmt:
		g.write("while ")
		g.header(st.Condition)
		g.block(st.Stmts, st.LastLine())
	case *ast.RepeatStmt:
		g.write("repeat")
		g.block(st.Stmts, st.Condition.Line())
		g.write(" until ")
		g.expr(st.Condition, 0)
	case *ast.IfStmt:
		g.ifStmt(st)
	case *ast.NumberForStmt:
		g.write("for " + g.checkName(st.Name, st.Line(), "local") + " = ")
		g.header(st.Init)
		g.write(", ")
		g.header(st.Limit)
		if st.Step != nil {
			g.write(", ")
			g.header(st.Step)
		}
		g.block(st.Stmts, st.LastLine())
	case *ast.GenericForStmt:
		for _, name := range st.Names {
			g.checkName(name, st.Line(), "local")
		}
		g.write("for " + strings.Join(st.Names, ", ") + " in ")
		for i, expr := range st.Exprs {
			if i > 0 {
				g.write(", ")
			}
			g.header(expr)
		}
		g.block(st.Stmts, st.LastLine())
	case *ast.FuncDefStmt:
		g.funcDef(st)
	case *ast.ReturnStmt:
		g.write("return")
		if len(st.Exprs) > 0 {
			g.write(" ")
			start := len(g.buf)
			g.exprList(st.Exprs)
			// `return (a), b` / `return () -> {}, b` would parse as returning
			// only the first value, so parenthesize the list.
			first := bytes.TrimLeft(g.buf[start:], " \n")
			if len(st.Exprs) > 1 && len(first) > 0 && first[0] == '(' {
				g.buf = append(g.buf[:start], append([]byte{'('}, g.buf[start:]...)...)
				g.write(")")
			}
		}
	case *ast.BreakStmt:
		g.write("break")
	case *ast.LabelStmt:
		g.write("::" + g.checkName(st.Name, st.Line(), "label") + "::")
	case *ast.GotoStmt:
		g.write("goto " + g.checkName(st.Label, st.Line(), "label"))
	default:
		panic(fmt.Sprintf("mngen: unknown statement %T", stmt))
	}
}

func (g *generator) localAssign(st *ast.LocalAssignStmt) {
	for _, name := range st.Names {
		g.checkName(name, st.Line(), "local")
	}

	// The Lua parser produces this for both `local function f()` and `local f
	// = function()`, which gopher-lua compiles the same (f is in scope in its
	// body).
	if len(st.Names) == 1 && len(st.Exprs) == 1 {
		if fn, ok := st.Exprs[0].(*ast.FunctionExpr); ok {
			g.write("local function " + st.Names[0])
			g.funcBody(fn, false)
			return
		}
	}

	g.write("local " + strings.Join(st.Names, ", "))
	if len(st.Exprs) > 0 {
		g.write(" = ")
		g.exprList(st.Exprs)
	}
}

func (g *generator) ifStmt(st *ast.IfStmt) {
	root := st
	g.write("if ")
	g.header(st.Condition)
	g.write(" {")
	g.depth++
	g.stmts(st.Then)

	for {
		if len(st.Else) == 1 {
			if elseif, ok := st.Else[0].(*ast.IfStmt); ok {
				g.depth--
				g.sync(elseif.Line())
				g.keyword("} elseif ")
				g.header(elseif.Condition)
				g.write(" {")
				g.depth++
				g.stmts(elseif.Then)
				st = elseif
				continue
			}
		}

		if len(st.Else) > 0 {
			g.depth--
			g.sync(g.elseLine(st.Else[0].Line()))
			g.keyword("} else {")
			g.depth++
			g.stmts(st.Else)
		}

		g.depth--
		g.sync(root.LastLine())
		g.keyword("}")
		return
	}
}

// elseLine returns the line of the `else` before a statement on `line`.
func (g *generator) elseLine(line int) int {
	i := sort.SearchInts(g.sc.elses, line+1) // first `else` after line
	if i == 0 {
		return 0
	}
	return g.sc.elses[i-1]
}

func (g *generator) funcDef(st *ast.FuncDefStmt) {
	line := st.Line()
	if st.Name.Func != nil {
		g.write("function " + g.funcPath(st.Name.Func, line))
	} else {
		if !isName(st.Name.Method) {
			g.errorf(line, "method name '%s' is a Midnight keyword (rename it)", st.Name.Method)
		}
		g.write("function " + g.funcPath(st.Name.Receiver, line) + ":" + st.Name.Method)
	}
	g.funcBody(st.Func, false)
}

// funcPath returns `a.b.c` for a function declaration name.
func (g *generator) funcPath(expr ast.Expr, line int) string {
	switch ex := expr.(type) {
	case *ast.IdentExpr:
		return g.checkName(ex.Value, line, "global")
	case *ast.AttrGetExpr:
		if key, ok := ex.Key.(*ast.StringExpr); ok {
			if !isName(key.Value) {
				g.errorf(line, "function name '%s' is a Midnight keyword (rename it)", key.Value)
			}
			return g.funcPath(ex.Object, line) + "." + key.Value
		}
	}
	panic(fmt.Sprintf("mngen: unexpected function name %T", expr))
}

// funcBody writes `(params) { body }`, or `(params) -> { body }` for a
// function expression (arrow).
func (g *generator) funcBody(fn *ast.FunctionExpr, arrow bool) {
	params := make([]string, 0, len(fn.ParList.Names)+1)
	for _, name := range fn.ParList.Names {
		params = append(params, g.checkName(name, fn.Line(), "parameter"))
	}
	if fn.ParList.HasVargs {
		params = append(params, "...")
	}

	end := g.open()
	g.write(strings.Join(params, ", "))
	end()
	if arrow {
		g.write(" ->")
	}
	g.block(fn.Stmts, fn.LastLine())
}

/* }}} */
