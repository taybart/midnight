package parse

import (
	"github.com/taybart/midnight/ast"
)

// `catch`: `<expr> catch <handler>` evaluates expr in protected mode (w/
// pcall), and if it throws, the handler instead:
//
//	local cfg = load_config(path) catch DEFAULTS              -- a fallback value
//	local n = tonumber(s) catch e { log(e) }                  -- a value w/ the error
//	local data = json.decode(body) catch e { return nil, e }  -- statements that exit
//	local x = risky() catch nil                               -- ignore the error
//	risky() catch e { log.warn(e) }                           -- statement
//
// The handler is one of:
//
//   - an expression: the value when expr throws.
//   - `[name] { expression }`: the same, w/ the error as `name`.
//   - `[name] { statements }`: statements that exit (they end in `return`,
//     `break`, `continue`, `goto`, or `error(...)`), since there's no value.
//     They run in the enclosing function, so `return` returns from it and
//     `break` / `continue` apply to the enclosing loop. This only works as the
//     (first) value of a declaration, assignment, or return, where the catch
//     is lowered to statements (see hoistCond).
//
// As a statement (`f() catch [name] { ... }`), the block is any statements.
//
// Like if / match expressions, it's lowered to statements that hand its value
// to a target (see lowerableExpr):
//
//	local data = json.decode(body) catch e { return nil, e }
//
//	local tmp
//	do
//	  local ok, v = pcall(function() return json.decode(body) end)
//	  if ok then tmp = v else local e = v; return nil, e end
//	end
//	local data = tmp
//
// expr is truncated to one value (like parentheses), and so is the value of
// the handler. `catch` binds looser than any operator (`a + f() catch 0`
// protects `a + f()`), and chains (`a() catch b() catch c` tries a, then b).

// midnightCatch is a parsed catch.
type midnightCatch struct {
	line, lastLine int
	// pcall's arguments: the protected function, then its arguments
	protected []ast.Expr
	// the error's name, or ""
	name string
	// the handler's value, or nil when it's statements
	value ast.Expr
	stmts []ast.Stmt
	// lowered in the enclosing function at least once (see hoist). A catch
	// whose handler exits must be: in a function of its own, its `return`
	// would return from that function.
	hoisted bool
}

func (c *midnightCatch) lower(p *midnightParser, target midnightTarget) []ast.Stmt {
	if p.hoisting {
		c.hoisted = true
	}
	line := c.line
	ok, v := p.newTmpName(), p.newTmpName()

	var handler []ast.Stmt
	if c.name != "" {
		handler = append(handler, midnightLocal([]string{c.name}, []ast.Expr{midnightIdent(v, line)}, line))
	}
	if c.value != nil {
		handler = append(handler, p.lowerValue(c.value, line, target)...)
	} else {
		handler = append(handler, c.stmts...)
	}

	pcall := midnightCall(midnightIdent("pcall", line), c.protected, line)
	check := midnightLine(&ast.IfStmt{
		Condition: midnightIdent(ok, line),
		Then:      []ast.Stmt{target(midnightIdent(v, line), line)},
		Else:      handler,
	}, line)
	check.SetLastLine(c.lastLine)
	block := midnightLine(&ast.DoBlockStmt{Stmts: []ast.Stmt{
		midnightLocal([]string{ok, v}, []ast.Expr{pcall}, line),
		check,
	}}, line)
	block.SetLastLine(c.lastLine)
	return []ast.Stmt{block}
}

// isCatch checks for `catch` after an expression. `catch` is a contextual
// keyword: it's a name everywhere else. On the line after the expression, it's
// still a name when followed by anything that continues one at the start of a
// statement (ex. `catch = 1`, `catch.x = 1`, `catch(x)`).
func (p *midnightParser) isCatch() bool {
	if p.cur.Type != midnightWord || p.cur.Value != "catch" {
		return false
	}
	if p.cur.Line == p.prevLine() {
		return true
	}
	next := p.tokens[p.index+1]
	if next.Type != midnightSymbol {
		return true
	}
	switch next.Value {
	case "=", ",", ".", "[", ":", "(":
		return false
	}
	return !midnightAssignmentOperators[next.Value] || !isMidnightSyntax(p.tokens[p.index+2], "=")
}

// protected returns pcall's arguments for evaluating expr: `f, a, b` for
// `f(a, b)` when evaluating f and its arguments can't throw (they're names or
// constants), or else expr in a function.
func (p *midnightParser) protected(expr ast.Expr, line int) []ast.Expr {
	if call, ok := expr.(*ast.FuncCallExpr); ok && call.Receiver == nil && midnightIsSimple(call.Func) {
		simple := true
		for _, arg := range call.Args {
			simple = simple && midnightIsSimple(arg)
		}
		if simple {
			return append([]ast.Expr{call.Func}, call.Args...)
		}
	}

	parList := &ast.ParList{Names: []string{}, HasVargs: p.isVarargs}
	// (an if / match / catch expression is lowered into the function, ex. the
	// first catch of `a() catch b() catch c`)
	body := p.lowerValue(expr, line, midnightCondReturn)
	fn := midnightLine(&ast.FunctionExpr{ParList: parList, Stmts: body}, line)
	fn.SetLastLine(p.prevLine())
	args := []ast.Expr{fn}
	if p.isVarargs { // so `...` in expr is the enclosing function's
		args = append(args, midnightLine(&ast.Comma3Expr{}, line))
	}
	return args
}

// midnightIsSimple checks for an expression whose evaluation can't throw.
func midnightIsSimple(expr ast.Expr) bool {
	switch expr.(type) {
	case *ast.IdentExpr, ast.ConstExpr, *ast.Comma3Expr:
		return true
	}
	return false
}

// catchBodyStart checks for a catch body (`{`, or a name and `{`), unless the
// `{` starts the block of the statement whose header this is.
func (p *midnightParser) catchBodyStart() bool {
	i := p.index
	if p.cur.Type == midnightWord && !midnightKeywords[p.cur.Value] && !midnightTerminals[p.cur.Value] {
		i++
	}
	token := p.tokens[i]
	return token.Type == midnightSymbol && token.Value == "{" && i != p.headerBraceIndex
}

// catchExpression parses `catch <handler>` after lhs.
func (p *midnightParser) catchExpression(lhs ast.Expr) ast.Expr {
	c := &midnightCatch{line: p.cur.Line}
	p.consume() // catch
	c.protected = p.protected(lhs, c.line)

	if p.catchBodyStart() {
		p.catchBody(c, true)
	} else {
		if p.cur.Type == midnightEOF {
			p.throwEOF("unexpected eof (expected the catch's value or block)")
		}
		c.value = midnightSingle(p.expression(midnightCatchPrec + 1))
	}
	c.lastLine = p.prevLine()

	if c.value == nil {
		p.exitingCatches = append(p.exitingCatches, c)
	}
	return p.lowerableExpr(c, c.line)
}

// catchStatement parses `<call> catch [name] { ... }` as a statement.
func (p *midnightParser) catchStatement(call ast.Expr) ast.Stmt {
	c := &midnightCatch{line: p.cur.Line}
	p.consume() // catch
	c.protected = p.protected(call, c.line)

	if !p.catchBodyStart() {
		p.throw(p.cur.Line, "a catch statement needs a block, ex. `f() catch e { log(e) }` (`f() catch {}` ignores the error)")
	}
	p.catchBody(c, false)
	line := c.line

	pcall := midnightCall(midnightIdent("pcall", line), c.protected, line)
	if c.name == "" && len(c.stmts) == 0 { // `f() catch {}`
		return midnightLine(&ast.FuncCallStmt{Expr: pcall}, line)
	}

	ok, v := p.newTmpName(), p.newTmpName()
	if c.name != "" {
		v = c.name
	}
	check := midnightLine(&ast.IfStmt{
		Condition: midnightLine(&ast.UnaryNotOpExpr{Expr: midnightIdent(ok, line)}, line),
		Then:      c.stmts,
		Else:      []ast.Stmt{},
	}, line)
	check.SetLastLine(p.prevLine())
	block := midnightLine(&ast.DoBlockStmt{Stmts: []ast.Stmt{
		midnightLocal([]string{ok, v}, []ast.Expr{pcall}, line),
		check,
	}}, line)
	block.SetLastLine(p.prevLine())
	return block
}

// catchBody parses `[name] { ... }`. As an expression, the body is one
// expression (the value), or statements that exit.
func (p *midnightParser) catchBody(c *midnightCatch, isExpression bool) {
	p.pushScope()
	defer p.popScope()

	if !p.is("{") {
		c.name = p.name()
		p.declare(c.name, midnightScopeLocal)
	}

	bodyLine := p.cur.Line
	p.expect("{")

	if isExpression {
		if p.is("}") {
			p.throw(p.cur.Line, "expected the catch's value, got '}' (`catch nil` ignores the error, `catch { {} }` is an empty table)")
		}
		value, isList := p.catchValue()
		if value != nil {
			c.value = value
			p.expect("}")
			return
		}
		if isList { // ex. `catch { 'a', 'b' }`, if it isn't statements
			defer func() {
				if e, ok := recover().(*Error); ok {
					e.Message += " (a catch block's value is one expression; for a table, use `catch { { ... } }`)"
					panic(e)
				} else if e != nil {
					panic(e)
				}
			}()
		}
	}

	c.stmts = p.block()
	p.expect("}")

	if isExpression && !midnightExits(c.stmts) {
		p.throw(bodyLine, "a catch block w/ statements must end in 'return', 'break', 'continue', 'goto', or 'error(...)', since it has no value (for a value, the block is one expression)")
	}
}

// catchValue parses a catch body that's one expression, or returns nil (and
// parses nothing) if it's statements. isList reports a body that starts w/ an
// expression and a `,` (a list of values, unless it's an assignment).
func (p *midnightParser) catchValue() (value ast.Expr, isList bool) {
	if p.cur.Type == midnightWord && midnightKeywords[p.cur.Value] && !p.is("if") || p.is("::") {
		return nil, false
	}

	saved := *p
	value, ok := p.attempt(func() ast.Expr { return p.expression(1) })
	if ok && p.is("}") {
		return midnightSingle(value), false
	}
	isList = ok && p.is(",")

	// statements: back to the start of the body (keeping the temporary names
	// unique)
	counter := p.tmpCounter
	*p = saved
	p.tmpCounter = counter
	return nil, isList
}

// attempt parses w/ f, returning false if it throws a parse error.
func (p *midnightParser) attempt(f func() ast.Expr) (expr ast.Expr, ok bool) {
	defer func() {
		if e := recover(); e != nil {
			if _, isParseError := e.(*Error); !isParseError {
				panic(e)
			}
			ok = false
		}
	}()
	return f(), true
}

// midnightExits checks that statements end by leaving the block: a return,
// break, continue, goto, error(...), or an if / do block whose branches all
// do.
func midnightExits(stmts []ast.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch last := stmts[len(stmts)-1].(type) {
	case *ast.ReturnStmt, *ast.BreakStmt, *ast.GotoStmt:
		return true
	case *ast.FuncCallStmt:
		call, ok := last.Expr.(*ast.FuncCallExpr)
		if !ok || call.Receiver != nil {
			return false
		}
		fn, ok := call.Func.(*ast.IdentExpr)
		return ok && fn.Value == "error"
	case *ast.DoBlockStmt:
		return midnightExits(last.Stmts)
	case *ast.IfStmt:
		return midnightExits(last.Then) && midnightExits(last.Else)
	}
	return false
}

// checkExitingCatches throws for a catch whose handler exits, but that's
// never lowered in the enclosing function (ex. it's an argument), where its
// `return` would return from the function it's lowered into instead.
func (p *midnightParser) checkExitingCatches() {
	for _, c := range p.exitingCatches {
		if !c.hoisted {
			p.throw(c.line, "a catch block that exits (w/ 'return', 'break', ...) only works as the value of a declaration, assignment, or return (assign it to a local first)")
		}
	}
}

// header parses a statement header (ex. `if <header> {`) w/ f. A catch in the
// header can't have a body that starts w/ the statement's block, so
// `if f() catch x {` is `f() catch x`, then the if's block.
func (p *midnightParser) header(f func()) {
	old := p.headerBraceIndex
	p.headerBraceIndex = p.findHeaderBrace()
	defer func() { p.headerBraceIndex = old }()
	f()
}

// headerExpression parses an expression in a statement header (see header).
func (p *midnightParser) headerExpression() (expr ast.Expr) {
	p.header(func() { expr = p.expression(1) })
	return expr
}

// findHeaderBrace finds the first `{` from the current token, outside of
// parentheses and brackets (or -1).
func (p *midnightParser) findHeaderBrace() int {
	for i := p.index; ; {
		token := p.tokens[i]
		switch {
		case token.Type == midnightEOF:
			return -1
		case token.Type != midnightSymbol:
			i++
		case token.Value == "{":
			return i
		case token.Value == "(" || token.Value == "[":
			_, i = p.lookPastSurround(i)
		default:
			i++
		}
	}
}

// Precedence of catch, looser than any operator.
const midnightCatchPrec = 1
