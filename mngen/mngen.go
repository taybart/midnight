// Package mngen converts Lua source to Midnight (the reverse of luagen).
//
// The conversion is a syntax translation: the output is the same program, w/
// Midnight syntax (`{ }` blocks, `->` functions, `&&` / `||` / `!` / `!=`),
// not rewritten to use Midnight features. Comments and line breaks are kept.
//
// Code that has no exact Midnight equivalent isn't converted. Instead, Convert
// returns an error for each problem, ex. a name that is a Midnight keyword
// (`continue`, `global`, `module`, as in Lua 5.1's `module(...)`).
//
// Every result is checked: the output must parse to the same program as the
// input, or Convert fails. The only difference allowed is the grouping of
// `..` chains (`a .. b .. c` is `a .. (b .. c)` in Lua and `(a .. b) .. c` in
// Midnight), which gives the same result unless a `__concat` metamethod
// depends on it.
package mngen

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/parse"
)

// Error is a problem converting a line of Lua.
type Error struct {
	Source  string
	Line    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.Source, e.Line, e.Message)
}

// Errors are all the problems found converting a file.
type Errors []*Error

func (es Errors) Error() string {
	msgs := make([]string, len(es))
	for i, e := range es {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Convert converts Lua source to Midnight. name is the source's name in
// errors. The error is an Errors if the Lua uses something that can't be
// converted, or another error if it doesn't parse.
func Convert(source []byte, name string) ([]byte, error) {
	text := strings.ReplaceAll(string(source), "\r\n", "\n")

	// Lua skips a first line starting w/ `#`, Midnight only `#!` (which scan
	// keeps as a comment).
	code := text
	if strings.HasPrefix(text, "#") {
		if !strings.HasPrefix(text, "#!") {
			return nil, Errors{{Source: name, Line: 1, Message: "a first line starting w/ '#' must be a '#!' line in Midnight"}}
		}
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			end = len(text)
		}
		code = text[end:]
	}

	chunk, err := parse.Parse(strings.NewReader(code), name)
	if err != nil {
		return nil, err
	}

	g := &generator{source: name, line: 1, fresh: true, sc: scan(text), usedParens: map[int]int{}, usedBraces: map[int]int{}}
	g.stmts(chunk)
	g.finish()

	if len(g.errs) > 0 {
		sort.SliceStable(g.errs, func(i, j int) bool { return g.errs[i].Line < g.errs[j].Line })
		return nil, g.errs
	}

	formatted, err := format.Format(g.buf, format.Options{})
	if err != nil {
		return nil, Errors{{Source: name, Line: errorLine(err), Message: "conversion bug, output doesn't parse: " + errorMessage(err)}}
	}

	if line, ok := verify(chunk, formatted); !ok {
		return nil, Errors{{Source: name, Line: line, Message: "conversion bug, output parses to a different program"}}
	}

	return formatted, nil
}

type generator struct {
	source string
	buf    []byte

	// Current output line (output lines follow the source's).
	line int
	// Block depth, for indentation
	depth int
	// Whether we are at the start of a line (nothing written yet)
	fresh bool
	// Line the last statement ended on (to separate statements on one line).
	stmtEnd int

	sc          *scanned
	nextComment int
	// Openers (see scanned) used so far, by line.
	usedParens, usedBraces map[int]int

	errs Errors
}

func (g *generator) errorf(line int, format string, args ...any) {
	g.errs = append(g.errs, &Error{Source: g.source, Line: line, Message: fmt.Sprintf(format, args...)})
}

/* Output {{{ */

func (g *generator) write(s string) {
	if g.fresh {
		g.buf = append(g.buf, strings.Repeat("  ", g.depth)...)
	}
	g.buf = append(g.buf, s...)
	g.line += strings.Count(s, "\n")
	g.fresh = false
}

// space separates tokens, unless we are at the start of a line.
func (g *generator) space() {
	if !g.fresh {
		g.write(" ")
	}
}

// newline ends the current line, after the comments on it.
func (g *generator) newline() {
	line := g.line
	g.flushComments()
	if g.line > line {
		return // (a multiline comment moved to the next line)
	}
	g.buf = append(g.buf, '\n')
	g.line++
	g.fresh = true
}

// flushComments writes the source's comments up to the current line.
func (g *generator) flushComments() {
	for g.nextComment < len(g.sc.comments) && g.sc.comments[g.nextComment].line <= g.line {
		g.space()
		g.write(g.sc.comments[g.nextComment].text)
		g.nextComment++
	}
}

// sync moves the output to `line` (never backwards), to keep the source's
// line breaks (and comments).
func (g *generator) sync(line int) {
	for g.line < line {
		g.newline()
	}
}

func (g *generator) finish() {
	if n := len(g.sc.comments); n > g.nextComment {
		g.sync(g.sc.comments[n-1].line)
	}
	g.flushComments()
	g.write("\n")
}

// keyword writes a keyword, separated from the previous token.
func (g *generator) keyword(keyword string) {
	g.space()
	g.write(keyword)
}

// opener returns the line of the closing token for the next `(` / `{` in the
// source on the current line (or 0 if unknown).
func (g *generator) opener(openers map[int][]*opener, used map[int]int) int {
	i := used[g.line]
	if i >= len(openers[g.line]) {
		return 0
	}
	used[g.line]++
	return openers[g.line][i].closeLine
}

// open writes a `(` that is in the source, and returns a function writing
// its `)`, on the same line as in the source.
func (g *generator) open() func() {
	closeLine := g.opener(g.sc.parens, g.usedParens)
	g.write("(")
	return func() {
		g.sync(closeLine)
		g.write(")")
	}
}

/* }}} */

/* Names {{{ */

// Words that are names in Lua but keywords in Midnight.
var midnightKeywords = map[string]bool{"continue": true, "global": true, "module": true}

// checkName checks a name (variable, parameter or label) is valid in Midnight.
func (g *generator) checkName(name string, line int, what string) string {
	if midnightKeywords[name] {
		hint := "rename it"
		switch {
		case what == "label" && name == "continue":
			hint = "use Midnight's `continue` instead of `goto continue`"
		case name == "module" && what == "global":
			hint = "Lua 5.1's module() isn't supported, return a table instead"
		}
		g.errorf(line, "%s '%s' is a Midnight keyword (%s)", what, name, hint)
	}
	return name
}

// isName reports whether s can be written as a name in Midnight (ex. a table
// key `t.s` or `{ s = 1 }`).
func isName(s string) bool {
	if s == "" || midnightKeywords[s] || luaKeywords[s] {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true, "or": true,
	"repeat": true, "return": true, "then": true, "true": true, "until": true,
	"while": true,
}

/* }}} */

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

/* Expressions {{{ */

// Operator precedences (higher binds tighter), the same in Lua and Midnight.
const (
	precOr = iota + 1
	precAnd
	precCompare
	precConcat
	precAdd
	precMul
	precUnary
	precPow
)

var arithmeticPrecs = map[string]int{
	"+": precAdd, "-": precAdd, "*": precMul, "/": precMul, "%": precMul, "^": precPow,
}

func (g *generator) exprList(exprs []ast.Expr) {
	for i, expr := range exprs {
		if i > 0 {
			g.write(", ")
		}
		g.expr(expr, 0)
	}
}

// header writes an expression in a statement header (ex. `if x {`), where a
// `{` outside of parentheses would start the block.
func (g *generator) header(expr ast.Expr) {
	if hasBrace(expr) {
		g.write("(")
		g.expr(expr, 0)
		g.write(")")
	} else {
		g.expr(expr, 0)
	}
}

// hasBrace reports whether an expression is written w/ a `{` outside of
// parentheses / brackets.
func hasBrace(expr ast.Expr) bool {
	switch ex := expr.(type) {
	case *ast.TableExpr, *ast.FunctionExpr:
		return true
	case *ast.AttrGetExpr:
		return isPrefix(ex.Object) && hasBrace(ex.Object)
	case *ast.FuncCallExpr:
		if ex.AdjustRet {
			return false
		}
		if ex.Func != nil {
			return isPrefix(ex.Func) && hasBrace(ex.Func)
		}
		return isPrefix(ex.Receiver) && hasBrace(ex.Receiver)
	case *ast.LogicalOpExpr:
		return hasBrace(ex.Lhs) || hasBrace(ex.Rhs)
	case *ast.RelationalOpExpr:
		return hasBrace(ex.Lhs) || hasBrace(ex.Rhs)
	case *ast.StringConcatOpExpr:
		return hasBrace(ex.Lhs) || hasBrace(ex.Rhs)
	case *ast.ArithmeticOpExpr:
		return hasBrace(ex.Lhs) || hasBrace(ex.Rhs)
	case *ast.UnaryMinusOpExpr:
		return hasBrace(ex.Expr)
	case *ast.UnaryNotOpExpr:
		return hasBrace(ex.Expr)
	case *ast.UnaryLenOpExpr:
		return hasBrace(ex.Expr)
	}
	return false
}

// binop writes `lhs op rhs`, parenthesized if its precedence is lower than
// `minPrec`.
func (g *generator) binop(op string, prec int, rightAssoc bool, lhs, rhs ast.Expr, minPrec int) {
	if prec < minPrec {
		defer g.open()()
	}

	lhsPrec, rhsPrec := prec, prec+1
	if rightAssoc {
		lhsPrec, rhsPrec = prec+1, prec
	}

	g.expr(lhs, lhsPrec)
	g.write(" " + op + " ")
	g.expr(rhs, rhsPrec)
}

// concat writes a `..` chain w/o parentheses (see the package docs).
func (g *generator) concat(ex *ast.StringConcatOpExpr, minPrec int) {
	if precConcat < minPrec {
		defer g.open()()
	}

	var operands []ast.Expr
	var flatten func(ast.Expr)
	flatten = func(expr ast.Expr) {
		if c, ok := expr.(*ast.StringConcatOpExpr); ok {
			flatten(c.Lhs)
			flatten(c.Rhs)
		} else {
			operands = append(operands, expr)
		}
	}
	flatten(ex)

	for i, operand := range operands {
		if i > 0 {
			g.write(" .. ")
		}
		g.expr(operand, precConcat+1)
	}
}

func (g *generator) unop(op string, operand ast.Expr, minPrec int) {
	if precUnary < minPrec {
		defer g.open()()
	}

	g.write(op)
	if _, ok := operand.(*ast.UnaryMinusOpExpr); ok && op == "-" {
		g.write(" ") // `--` would be a comment
	}
	g.expr(operand, precUnary)
}

func (g *generator) expr(expr ast.Expr, minPrec int) {
	g.sync(expr.Line())

	switch ex := expr.(type) {
	case *ast.NilExpr:
		g.write("nil")
	case *ast.TrueExpr:
		g.write("true")
	case *ast.FalseExpr:
		g.write("false")
	case *ast.NumberExpr:
		g.write(number(ex.Value))
	case *ast.StringExpr:
		g.string(ex.Value)
	case *ast.Comma3Expr:
		if ex.AdjustRet {
			end := g.open()
			g.write("...")
			end()
		} else {
			g.write("...")
		}
	case *ast.IdentExpr:
		g.write(g.checkName(ex.Value, ex.Line(), "global"))
	case *ast.AttrGetExpr:
		g.prefixExpr(ex.Object)
		g.index(ex.Key)
	case *ast.TableExpr:
		g.table(ex)
	case *ast.FuncCallExpr:
		if ex.AdjustRet {
			defer g.open()()
		}
		g.call(ex)
	case *ast.LogicalOpExpr:
		if ex.Operator == "or" {
			g.binop("||", precOr, false, ex.Lhs, ex.Rhs, minPrec)
		} else {
			g.binop("&&", precAnd, false, ex.Lhs, ex.Rhs, minPrec)
		}
	case *ast.RelationalOpExpr:
		op := ex.Operator
		if op == "~=" {
			op = "!="
		}
		g.binop(op, precCompare, false, ex.Lhs, ex.Rhs, minPrec)
	case *ast.StringConcatOpExpr:
		g.concat(ex, minPrec)
	case *ast.ArithmeticOpExpr:
		prec := arithmeticPrecs[ex.Operator]
		g.binop(ex.Operator, prec, prec == precPow, ex.Lhs, ex.Rhs, minPrec)
	case *ast.UnaryMinusOpExpr:
		g.unop("-", ex.Expr, minPrec)
	case *ast.UnaryNotOpExpr:
		g.unop("!", ex.Expr, minPrec)
	case *ast.UnaryLenOpExpr:
		g.unop("#", ex.Expr, minPrec)
	case *ast.FunctionExpr:
		g.funcBody(ex, true)
	default:
		panic(fmt.Sprintf("mngen: unknown expression %T", expr))
	}
}

// isPrefix reports whether an expression can be indexed or called w/o
// parentheses.
func isPrefix(expr ast.Expr) bool {
	switch ex := expr.(type) {
	case *ast.IdentExpr, *ast.AttrGetExpr:
		return true
	case *ast.FuncCallExpr:
		return true
	case *ast.Comma3Expr:
		return ex.AdjustRet // `(...)`
	}
	return false
}

// prefixExpr writes an expression that is indexed or called.
func (g *generator) prefixExpr(expr ast.Expr) {
	if isPrefix(expr) {
		g.expr(expr, 0)
		return
	}
	defer g.open()()
	g.expr(expr, 0)
}

func (g *generator) index(key ast.Expr) {
	if str, ok := key.(*ast.StringExpr); ok && isName(str.Value) {
		g.write("." + str.Value)
		return
	}
	g.write("[")
	g.expr(key, 0)
	g.write("]")
}

func (g *generator) call(ex *ast.FuncCallExpr) {
	if ex.Func != nil {
		g.prefixExpr(ex.Func)
	} else {
		if !isName(ex.Method) {
			g.errorf(ex.Line(), "method name '%s' is a Midnight keyword (rename it)", ex.Method)
		}
		g.prefixExpr(ex.Receiver)
		g.write(":" + ex.Method)
	}

	end := g.open()
	g.exprList(ex.Args)
	end()
}

func (g *generator) table(ex *ast.TableExpr) {
	closeLine := g.opener(g.sc.braces, g.usedBraces)
	g.write("{")
	for i, field := range ex.Fields {
		if i > 0 {
			g.write(", ")
		}

		if field.Key != nil {
			if str, ok := field.Key.(*ast.StringExpr); ok && isName(str.Value) {
				g.sync(str.Line())
				g.write(str.Value)
			} else {
				g.sync(field.Key.Line())
				g.write("[")
				g.expr(field.Key, 0)
				g.write("]")
			}
			g.write(" = ")
		}

		g.expr(field.Value, 0)
	}
	g.sync(closeLine)
	g.write("}")
}

/* }}} */

/* Literals {{{ */

// number returns a number literal valid in Midnight (Lua allows `5.` and
// `5.e3`, Midnight needs a digit after the `.`).
func number(value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "0x") {
		return value
	}
	if i := strings.IndexByte(value, '.'); i >= 0 && (i == len(value)-1 || !isDigit(value[i+1])) {
		return value[:i+1] + "0" + value[i+1:]
	}
	return value
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// string writes a string literal. Strings that span lines in the source are
// written as block strings (`[[...]]`) when possible, to keep their lines.
func (g *generator) string(s string) {
	if leadingNewline, ok := g.sc.multiline[g.line]; ok {
		if lit, ok := blockString(s, leadingNewline); ok && !g.commentsWithin(strings.Count(lit, "\n")) {
			g.write(lit)
			return
		}
	}
	g.write(quote(s))
}

// commentsWithin reports whether the source has comments on the next n lines
// (including this one), which writing n newlines would skip past.
func (g *generator) commentsWithin(n int) bool {
	return g.nextComment < len(g.sc.comments) && g.sc.comments[g.nextComment].line < g.line+n
}

// blockString returns s as a block string, if it's multiline and can be one.
// In Midnight, `{` in a block string starts an interpolation, and `\{` / `\}`
// are escapes. leadingNewline starts it w/ a (skipped) newline.
func blockString(s string, leadingNewline bool) (string, bool) {
	if !strings.Contains(s, "\n") {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 32 && c != '\n' && c != '\t' || c == 127 {
			return "", false
		}
	}

	// Escape `{` as `\{`, and a `}` after a `\` (which would be read as `\}`).
	// A `\` that isn't before a brace is kept as is, so `\` + `\}` is `\}`.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '{':
			b.WriteString(`\{`)
		case c == '}' && i > 0 && s[i-1] == '\\':
			b.WriteString(`\}`)
		default:
			b.WriteByte(c)
		}
	}
	s = b.String()

	eq := ""
	for {
		closeQuote := "]" + eq + "]"
		if strings.Index(s+closeQuote, closeQuote) == len(s) {
			break
		}
		eq += "="
	}

	// (a newline right after the opening quote is skipped)
	if leadingNewline || strings.HasPrefix(s, "\n") {
		s = "\n" + s
	}
	return "[" + eq + "[" + s + "]" + eq + "]", true
}

// quote returns a quoted string literal. Single quoted strings have no
// interpolation, so they're preferred.
func quote(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}

	var b bytes.Buffer
	b.WriteByte(q)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == q || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '{' && q == '"':
			b.WriteString(`\{`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(q)
	return b.String()
}

/* }}} */
