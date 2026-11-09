package parse

import (
	"fmt"
	"io"
	"strconv"

	"github.com/taybart/midnight/ast"
)

// Midnight parser. Midnight is a Lua dialect, so instead
// of compiling to Lua source (like the Erde reference compiler, erde/compile.erde)
// we lower it directly into gopher-lua's (Lua 5.1) AST. Parsing mirrors the
// reference compiler, so the two should be read side by side.

var midnightKeywords = map[string]bool{
	"break": true, "continue": true, "do": true, "else": true, "elseif": true,
	"for": true, "function": true, "global": true, "if": true, "in": true,
	"local": true, "module": true, "repeat": true, "return": true,
	"until": true, "while": true,
}

var midnightTerminals = map[string]bool{
	"true": true, "false": true, "nil": true, "...": true,
}

type midnightBinop struct {
	prec       int
	rightAssoc bool
}

var midnightBinops = map[string]midnightBinop{
	"||": {3, false},
	"&&": {4, false},
	"==": {5, false}, "!=": {5, false}, "<=": {5, false}, ">=": {5, false}, "<": {5, false}, ">": {5, false},
	"|":  {6, false},
	"~":  {7, false},
	"&":  {8, false},
	"<<": {9, false}, ">>": {9, false},
	"..": {10, false},
	"+":  {11, false}, "-": {11, false},
	"*": {12, false}, "/": {12, false}, "//": {12, false}, "%": {12, false},
	"^": {14, true},
}

const midnightUnopPrec = 13

var midnightBitlibMethods = map[string]string{
	"|": "bor", "~": "bxor", "&": "band", "<<": "lshift", ">>": "rshift",
}

// Names for the library tables used by lowered operators. These are resolved
// like any other name, same as the reference compiler.
const (
	midnightBitlib     = "bit32"
	midnightModuleName = "_MODULE"
)

const (
	midnightScopeLocal  = "local"
	midnightScopeGlobal = "global"
	midnightScopeModule = "module"
)

type midnightLoop struct {
	continueLabel string
	hasContinue   bool
}

type midnightParser struct {
	source string
	tokens []midnightToken
	index  int
	cur    midnightToken

	blockDepth int
	scopes     []map[string]string
	tmpCounter int

	// Innermost loop, or nil outside of loops (reset at function boundaries).
	loop *midnightLoop

	opts MidnightOptions
	// Token index of the current match arm's `=>`, which ends the arm's
	// patterns instead of starting an arrow function (-1 outside of patterns).
	armArrowIndex int
	// Local holding the automatic markup factory (see markupFactory), or "" if
	// unused.
	markupRuntime string
	// if / match expressions, by the expression they were parsed to, so they
	// can be lowered to statements where they're used (see midnight_cond.go).
	conds map[ast.Expr]*midnightCond

	isVarargs           bool
	isModuleReturnBlock bool
	moduleReturnLine    int
	hasModuleDecls      bool
}

// Result of parsing an index chain (ex. `a.b[c]:d()`).
type midnightChain struct {
	expr       ast.Expr
	chainLen   int
	isCall     bool
	finalBase  ast.Expr // `a.b` in `a.b[c]`
	finalIndex ast.Expr // `c` in `a.b[c]`
}

// Result of parsing a destructure (ex. `{ a, b: c = 1 }`).
type midnightDestructure struct {
	tmpName string
	names   []string
	stmts   []ast.Stmt
}

// ParseMidnight parses Midnight source into a Lua chunk, which can be passed to
// `lua.Compile` like any chunk returned by `Parse`.
func ParseMidnight(reader io.Reader, name string) ([]ast.Stmt, error) {
	return ParseMidnightWithOptions(reader, name, MidnightOptions{})
}

// ParseMidnightWithOptions is like ParseMidnight, but w/ options (ex. the markup
// factory).
func ParseMidnightWithOptions(reader io.Reader, name string, opts MidnightOptions) (chunk []ast.Stmt, err error) {
	text, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}

	tokens, err := midnightTokenize(string(text), name)
	if err != nil {
		return nil, err
	}

	p := &midnightParser{
		source:              name,
		tokens:              tokens,
		cur:                 tokens[0],
		blockDepth:          1,
		scopes:              []map[string]string{{}},
		isVarargs:           true,
		isModuleReturnBlock: true,
		opts:                opts,
		armArrowIndex:       -1,
	}

	defer func() {
		if e := recover(); e != nil {
			if perr, ok := e.(*Error); ok {
				chunk, err = nil, perr
				return
			}
			panic(e)
		}
	}()

	return p.module(), nil
}

/* Parse helpers {{{ */

func (p *midnightParser) throw(line int, format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: p.source, Line: line},
		Message: fmt.Sprintf(format, args...),
		Token:   p.cur.Value,
	})
}

// throwEOF throws an error for input that ends early. These are reported at
// line `EOF` (same as the Lua parser), so callers can tell incomplete input
// apart from invalid input (ex. to continue reading lines in a REPL).
func (p *midnightParser) throwEOF(format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: p.source, Line: EOF},
		Message: fmt.Sprintf(format, args...),
	})
}

func (p *midnightParser) consume() string {
	value := p.cur.Value
	p.index++
	p.cur = p.tokens[p.index]
	return value
}

func (p *midnightParser) prevLine() int { return p.tokens[p.index-1].Line }

func isMidnightSyntax(token midnightToken, value string) bool {
	switch token.Type {
	case midnightSymbol, midnightWord, midnightInterpolation:
		return token.Value == value
	}
	return false
}

func (p *midnightParser) is(value string) bool { return isMidnightSyntax(p.cur, value) }

func (p *midnightParser) isArrow(index int) bool {
	token := p.tokens[index]
	return index != p.armArrowIndex && (isMidnightSyntax(token, "->") || isMidnightSyntax(token, "=>"))
}

func (p *midnightParser) branch(value string) bool {
	if p.is(value) {
		p.consume()
		return true
	}
	return false
}

func (p *midnightParser) expect(value string) {
	if p.cur.Type == midnightEOF {
		p.throwEOF("unexpected eof (expected %s)", value)
	}
	if !p.is(value) {
		p.throw(p.cur.Line, "expected '%s' got '%s'", value, p.cur.Value)
	}
	p.consume()
}

func (p *midnightParser) list(callback func()) {
	for {
		callback()
		if !p.branch(",") {
			return
		}
	}
}

// surroundList parses `open {item ,} close`, allowing a trailing comma.
func (p *midnightParser) surroundList(open, close string, allowEmpty bool, callback func()) {
	p.expect(open)
	if !p.is(close) || !allowEmpty {
		for {
			callback()
			if !p.branch(",") || p.is(close) {
				break
			}
		}
	}
	p.expect(close)
}

var midnightSurroundEnds = map[string]string{"(": ")", "[": "]", "{": "}"}

// lookPastSurround returns the token (and its index) following the surround
// (parens, brackets, or braces) starting at `start`.
func (p *midnightParser) lookPastSurround(start int) (midnightToken, int) {
	open := p.tokens[start]
	close := midnightSurroundEnds[open.Value]
	depth := 1

	i := start + 1
	for depth > 0 {
		token := p.tokens[i]
		if token.Type == midnightEOF {
			p.throwEOF("unexpected eof, missing '%s'", close)
		}
		if isMidnightSyntax(token, open.Value) {
			depth++
		} else if isMidnightSyntax(token, close) {
			depth--
		}
		i++
	}

	return p.tokens[i], i
}

func (p *midnightParser) newTmpName() string {
	p.tmpCounter++
	// Not a valid identifier, so it can never clash w/ user names.
	return "(midnight " + strconv.Itoa(p.tmpCounter) + ")"
}

/* }}} */

/* Scopes {{{ */

func (p *midnightParser) pushScope() { p.scopes = append(p.scopes, map[string]string{}) }

func (p *midnightParser) popScope() { p.scopes = p.scopes[:len(p.scopes)-1] }

func (p *midnightParser) declare(name, scope string) { p.scopes[len(p.scopes)-1][name] = scope }

func (p *midnightParser) lookup(name string) string {
	for i := len(p.scopes) - 1; i >= 0; i-- {
		if scope, ok := p.scopes[i][name]; ok {
			return scope
		}
	}
	return ""
}

// target returns the expression referring to `name` when declared w/ `scope`.
func (p *midnightParser) target(name, scope string, line int) ast.Expr {
	switch scope {
	case midnightScopeModule:
		return midnightAttr(midnightIdent(midnightModuleName, line), midnightString(name, line), line)
	case midnightScopeGlobal:
		return midnightAttr(midnightIdent("_G", line), midnightString(name, line), line)
	}
	return midnightIdent(name, line)
}

func (p *midnightParser) resolve(name string, line int) ast.Expr {
	return p.target(name, p.lookup(name), line)
}

/* }}} */

/* AST helpers {{{ */

func midnightLine[T ast.PositionHolder](node T, line int) T {
	node.SetLine(line)
	node.SetLastLine(line)
	return node
}

// closed sets the last line of a block statement to the closing brace.
func (p *midnightParser) closed(stmt ast.Stmt) ast.Stmt {
	stmt.SetLastLine(p.prevLine())
	return stmt
}

func midnightIdent(name string, line int) *ast.IdentExpr {
	return midnightLine(&ast.IdentExpr{Value: name}, line)
}

func midnightString(value string, line int) *ast.StringExpr {
	return midnightLine(&ast.StringExpr{Value: value}, line)
}

func midnightAttr(object, key ast.Expr, line int) *ast.AttrGetExpr {
	return midnightLine(&ast.AttrGetExpr{Object: object, Key: key}, line)
}

func midnightCall(fn ast.Expr, args []ast.Expr, line int) *ast.FuncCallExpr {
	return midnightLine(&ast.FuncCallExpr{Func: fn, Args: args}, line)
}

// midnightLibCall calls `lib.method(args...)`, truncated to a single value.
func midnightLibCall(lib, method string, args []ast.Expr, line int) *ast.FuncCallExpr {
	call := midnightCall(midnightAttr(midnightIdent(lib, line), midnightString(method, line), line), args, line)
	call.AdjustRet = true
	return call
}

func midnightAssign(lhs, rhs ast.Expr, line int) *ast.AssignStmt {
	return midnightLine(&ast.AssignStmt{Lhs: []ast.Expr{lhs}, Rhs: []ast.Expr{rhs}}, line)
}

func midnightLocal(names []string, exprs []ast.Expr, line int) *ast.LocalAssignStmt {
	if exprs == nil {
		exprs = []ast.Expr{}
	}
	return midnightLine(&ast.LocalAssignStmt{Names: names, Exprs: exprs}, line)
}

// midnightDefault compiles `if target == nil { target = value }`.
func midnightDefault(target, value ast.Expr, line int) *ast.IfStmt {
	condition := midnightLine(&ast.RelationalOpExpr{Operator: "==", Lhs: target, Rhs: midnightLine(&ast.NilExpr{}, line)}, line)
	return midnightLine(&ast.IfStmt{
		Condition: condition,
		Then:      []ast.Stmt{midnightAssign(target, value, line)},
		Else:      []ast.Stmt{},
	}, line)
}

func (p *midnightParser) binop(op string, line int, lhs, rhs ast.Expr) ast.Expr {
	switch op {
	case "||":
		return midnightLine(&ast.LogicalOpExpr{Operator: "or", Lhs: lhs, Rhs: rhs}, line)
	case "&&":
		return midnightLine(&ast.LogicalOpExpr{Operator: "and", Lhs: lhs, Rhs: rhs}, line)
	case "!=":
		return midnightLine(&ast.RelationalOpExpr{Operator: "~=", Lhs: lhs, Rhs: rhs}, line)
	case "==", "<", "<=", ">", ">=":
		return midnightLine(&ast.RelationalOpExpr{Operator: op, Lhs: lhs, Rhs: rhs}, line)
	case "..":
		return midnightLine(&ast.StringConcatOpExpr{Lhs: lhs, Rhs: rhs}, line)
	case "//":
		quotient := midnightLine(&ast.ArithmeticOpExpr{Operator: "/", Lhs: lhs, Rhs: rhs}, line)
		return midnightLibCall("math", "floor", []ast.Expr{quotient}, line)
	}

	if method, ok := midnightBitlibMethods[op]; ok {
		return midnightLibCall(midnightBitlib, method, []ast.Expr{lhs, rhs}, line)
	}

	return midnightLine(&ast.ArithmeticOpExpr{Operator: op, Lhs: lhs, Rhs: rhs}, line)
}

/* }}} */

/* Variables {{{ */

func (p *midnightParser) name() string {
	switch {
	case p.cur.Type == midnightEOF:
		p.throwEOF("unexpected eof")
	case p.cur.Type != midnightWord:
		p.throw(p.cur.Line, "unexpected token '%s'", p.cur.Value)
	case midnightKeywords[p.cur.Value]:
		p.throw(p.cur.Line, "unexpected keyword '%s'", p.cur.Value)
	case midnightTerminals[p.cur.Value]:
		p.throw(p.cur.Line, "unexpected builtin '%s'", p.cur.Value)
	}
	return p.consume()
}

func (p *midnightParser) arrayDestructure(scope string) *midnightDestructure {
	d := &midnightDestructure{tmpName: p.newTmpName()}
	index := 0

	p.surroundList("[", "]", false, func() {
		index++
		line := p.cur.Line

		name := p.name()
		d.names = append(d.names, name)

		target := p.target(name, scope, line)
		key := midnightLine(&ast.NumberExpr{Value: strconv.Itoa(index)}, line)
		d.stmts = append(d.stmts, midnightAssign(target, midnightAttr(midnightIdent(d.tmpName, line), key, line), line))

		if p.branch("=") {
			d.stmts = append(d.stmts, midnightDefault(target, p.expression(1), line))
		}
	})

	return d
}

func (p *midnightParser) mapDestructure(scope string) *midnightDestructure {
	d := &midnightDestructure{tmpName: p.newTmpName()}

	p.surroundList("{", "}", false, func() {
		line := p.cur.Line

		key := p.name()
		name := key
		if p.branch(":") {
			name = p.name()
		}
		d.names = append(d.names, name)

		target := p.target(name, scope, line)
		d.stmts = append(d.stmts, midnightAssign(target, midnightAttr(midnightIdent(d.tmpName, line), midnightString(key, line), line), line))

		if p.branch("=") {
			d.stmts = append(d.stmts, midnightDefault(target, p.expression(1), line))
		}
	})

	return d
}

// variable parses a name or destructure. Exactly one of the results is set.
func (p *midnightParser) variable(scope string) (string, *midnightDestructure) {
	if p.is("[") {
		return "", p.arrayDestructure(scope)
	} else if p.is("{") {
		return "", p.mapDestructure(scope)
	}
	return p.name(), nil
}

/* }}} */

/* Index chains {{{ */

func (p *midnightParser) indexChain(base ast.Expr) *midnightChain {
	chain := &midnightChain{expr: base}

	for {
		line := p.cur.Line

		switch {
		// Use newlines to infer whether the parentheses belong to a function call
		// or the next statement.
		case p.is("(") && line == p.prevLine():
			chain.isCall = true
			chain.expr = midnightCall(chain.expr, p.arguments(), line)
		case p.is("["):
			p.consume()
			chain.isCall = false
			chain.finalBase, chain.finalIndex = chain.expr, p.expression(1)
			chain.expr = midnightAttr(chain.expr, chain.finalIndex, line)
			p.expect("]")
		case p.is("."):
			p.consume()
			chain.isCall = false
			chain.finalBase, chain.finalIndex = chain.expr, midnightString(p.name(), line)
			chain.expr = midnightAttr(chain.expr, chain.finalIndex, line)
		case p.is(":"):
			p.consume()
			chain.isCall = true
			method := p.name()
			chain.expr = midnightLine(&ast.FuncCallExpr{Receiver: chain.expr, Method: method, Args: p.arguments()}, line)
		default:
			return chain
		}

		chain.chainLen++
	}
}

func (p *midnightParser) arguments() []ast.Expr {
	args := []ast.Expr{}
	p.surroundList("(", ")", true, func() {
		args = append(args, p.expression(1))
	})
	return args
}

/* }}} */

/* Strings {{{ */

func (p *midnightParser) stringExpr() ast.Expr {
	line := p.cur.Line
	delimiter := p.cur.Type
	parts := []ast.Expr{}

	p.consume() // opening quote

	for p.cur.Type != delimiter {
		switch {
		case p.cur.Type == midnightStringContent:
			parts = append(parts, midnightString(p.cur.Value, p.cur.Line))
			p.consume()
		case p.cur.Type == midnightInterpolation:
			interpolationLine := p.cur.Line
			p.consume() // {
			value := p.expression(1)
			if p.cur.Type == midnightFormatSpec {
				// `{x:%.2f}` -> string.format('%.2f', x)
				spec := midnightString(p.cur.Value, p.cur.Line)
				p.consume()
				p.expect("}")
				parts = append(parts, midnightLibCall("string", "format", []ast.Expr{spec, value}, interpolationLine))
				break
			}
			p.expect("}")
			parts = append(parts, midnightCall(midnightIdent("tostring", interpolationLine), []ast.Expr{value}, interpolationLine))
		default:
			p.throw(p.cur.Line, "unexpected token '%s'", p.cur.Value)
		}
	}

	p.consume() // closing quote

	if len(parts) == 0 {
		return midnightString("", line)
	}

	// `..` is right associative in Lua
	expr := parts[len(parts)-1]
	for i := len(parts) - 2; i >= 0; i-- {
		expr = midnightLine(&ast.StringConcatOpExpr{Lhs: parts[i], Rhs: expr}, line)
	}
	return expr
}

/* }}} */

/* Tables {{{ */

func (p *midnightParser) tableConstructor() ast.Expr {
	line := p.cur.Line
	fields := []*ast.Field{}

	p.surroundList("{", "}", true, func() {
		next := p.tokens[p.index+1]

		if p.is("[") {
			p.consume()
			key := p.expression(1)
			p.expect("]")
			p.expect("=")
			fields = append(fields, &ast.Field{Key: key, Value: p.expression(1)})
		} else if next.Type == midnightSymbol && next.Value == "=" {
			keyLine := p.cur.Line
			key := p.name()
			p.consume() // =
			fields = append(fields, &ast.Field{Key: midnightString(key, keyLine), Value: p.expression(1)})
		} else {
			fields = append(fields, &ast.Field{Value: p.expression(1)})
		}
	})

	return midnightLine(&ast.TableExpr{Fields: fields}, line)
}

/* }}} */

/* Returns {{{ */

// returnList parses the values of `return` (and arrow function expression
// bodies), where `(a, b)` returns multiple values.
func (p *midnightParser) returnList() []ast.Expr {
	if !p.is("(") {
		return p.expressionList()
	}

	limit, limitIndex := p.lookPastSurround(p.index)
	if isMidnightSyntax(limit, "->") || isMidnightSyntax(limit, "=>") {
		return []ast.Expr{p.arrowFunction()}
	}

	for i := p.index + 1; i < limitIndex; {
		token := p.tokens[i]
		if _, ok := midnightSurroundEnds[token.Value]; ok && token.Type == midnightSymbol {
			_, i = p.lookPastSurround(i)
		} else if token.Type == midnightSymbol && token.Value == "," {
			exprs := []ast.Expr{}
			p.surroundList("(", ")", false, func() {
				exprs = append(exprs, p.expression(1))
			})
			return exprs
		} else {
			i++
		}
	}

	return []ast.Expr{p.expression(1)}
}

func (p *midnightParser) returnStmt() ast.Stmt {
	line := p.cur.Line
	if p.isModuleReturnBlock {
		p.moduleReturnLine = line
	}

	p.consume() // return
	exprs := []ast.Expr{}

	if p.blockDepth == 1 {
		if p.cur.Type != midnightEOF {
			exprs = p.returnList()
		}
		if p.cur.Type != midnightEOF {
			p.throw(p.cur.Line, "expected '<eof>', got '%s'", p.cur.Value)
		}
	} else {
		if !p.is("}") {
			exprs = p.returnList()
		}
		if !p.is("}") {
			p.throw(p.cur.Line, "expected '}', got '%s'", p.cur.Value)
		}
	}

	if stmt := p.returnCond(exprs, line); stmt != nil {
		return stmt
	}
	return midnightLine(&ast.ReturnStmt{Exprs: exprs}, line)
}

/* }}} */

/* Functions {{{ */

// parameters parses a parameter list into the function scope (which must
// already be pushed). Returns statements to prepend to the function body for
// defaults / destructures.
func (p *midnightParser) parameters() (*ast.ParList, []ast.Stmt) {
	parList := &ast.ParList{Names: []string{}}
	prelude := []ast.Stmt{}

	p.surroundList("(", ")", true, func() {
		line := p.cur.Line

		if p.branch("...") {
			parList.HasVargs = true

			if p.cur.Type == midnightWord {
				name := p.name()
				p.declare(name, midnightScopeLocal)
				varargs := midnightLine(&ast.TableExpr{Fields: []*ast.Field{{Value: midnightLine(&ast.Comma3Expr{}, line)}}}, line)
				prelude = append(prelude, midnightLocal([]string{name}, []ast.Expr{varargs}, line))
			}

			p.branch(",")
			if !p.is(")") {
				p.throw(p.cur.Line, "expected ')' got '%s'", p.cur.Value)
			}
			return
		}

		name, destructure := p.variable(midnightScopeLocal)
		if destructure != nil {
			name = destructure.tmpName
			for _, n := range destructure.names {
				p.declare(n, midnightScopeLocal)
			}
		} else {
			p.declare(name, midnightScopeLocal)
		}

		parList.Names = append(parList.Names, name)

		if p.branch("=") {
			prelude = append(prelude, midnightDefault(midnightIdent(name, line), p.expression(1), line))
		}

		if destructure != nil {
			prelude = append(prelude, midnightLocal(destructure.names, nil, line))
			prelude = append(prelude, destructure.stmts...)
		}
	})

	return parList, prelude
}

// functionBlock parses `{ ... }` as a function body.
func (p *midnightParser) functionBlock() ([]ast.Stmt, int) {
	oldIsModuleReturnBlock, oldLoop := p.isModuleReturnBlock, p.loop
	p.isModuleReturnBlock, p.loop = false, nil

	p.expect("{")
	stmts := p.block()
	lastLine := p.cur.Line
	p.expect("}")

	p.isModuleReturnBlock, p.loop = oldIsModuleReturnBlock, oldLoop
	return stmts, lastLine
}

func (p *midnightParser) arrowFunction() ast.Expr {
	line := p.cur.Line
	oldIsVarargs := p.isVarargs

	p.pushScope()
	defer p.popScope()

	var parList *ast.ParList
	var prelude []ast.Stmt

	if p.is("(") {
		parList, prelude = p.parameters()
		p.isVarargs = parList.HasVargs
	} else {
		p.isVarargs = false
		parList = &ast.ParList{Names: []string{}}

		name, destructure := p.variable(midnightScopeLocal)
		if destructure != nil {
			parList.Names = append(parList.Names, destructure.tmpName)
			for _, n := range destructure.names {
				p.declare(n, midnightScopeLocal)
			}
			prelude = append(prelude, midnightLocal(destructure.names, nil, line))
			prelude = append(prelude, destructure.stmts...)
		} else {
			p.declare(name, midnightScopeLocal)
			parList.Names = append(parList.Names, name)
		}
	}

	fatArrow := false
	switch {
	case p.branch("->"):
	case p.branch("=>"):
		fatArrow = true
		parList.Names = append([]string{"self"}, parList.Names...)
	case p.cur.Type == midnightEOF:
		p.throwEOF("unexpected eof (expected '->' or '=>')")
	default:
		p.throw(p.cur.Line, "unexpected token '%s' (expected '->' or '=>')", p.cur.Value)
	}

	var body []ast.Stmt
	lastLine := line

	if p.is("{") {
		body, lastLine = p.functionBlock()
	} else {
		returnLine := p.cur.Line
		exprs := []ast.Expr{}
		if p.is("(") {
			exprs = p.returnList()
		} else {
			exprs = append(exprs, p.expression(1))
		}
		if stmt := p.returnCond(exprs, returnLine); stmt != nil {
			body = []ast.Stmt{stmt}
		} else {
			body = []ast.Stmt{midnightLine(&ast.ReturnStmt{Exprs: exprs}, returnLine)}
		}
		lastLine = p.prevLine()
	}

	p.isVarargs = oldIsVarargs

	fn := &ast.FunctionExpr{ParList: parList, Stmts: append(prelude, body...), FatArrow: fatArrow}
	fn.SetLine(line)
	fn.SetLastLine(lastLine)
	return fn
}

// functionBody parses the parameters and body of a function declaration.
func (p *midnightParser) functionBody(line int) *ast.FunctionExpr {
	oldIsVarargs := p.isVarargs

	p.pushScope()
	defer p.popScope()

	parList, prelude := p.parameters()
	p.isVarargs = parList.HasVargs
	body, lastLine := p.functionBlock()
	p.isVarargs = oldIsVarargs

	fn := &ast.FunctionExpr{ParList: parList, Stmts: append(prelude, body...)}
	fn.SetLine(line)
	fn.SetLastLine(lastLine)
	return fn
}

func (p *midnightParser) functionDeclaration(scope string) ast.Stmt {
	line := p.cur.Line
	p.consume() // function

	baseName := p.name()

	if scope != "" && (p.is(".") || p.is(":")) {
		// Lua does not allow scope for table values (ex. `local function a.b()`)
		p.throw(p.cur.Line, "cannot use scopes for table values")
	}

	if scope == midnightScopeLocal {
		p.declare(baseName, scope)
		return midnightLocal([]string{baseName}, []ast.Expr{p.functionBody(line)}, line)
	}

	var signature ast.Expr
	if scope != "" {
		p.declare(baseName, scope)
		signature = p.target(baseName, scope, line)
	} else {
		signature = p.resolve(baseName, line)
	}

	for p.branch(".") {
		signature = midnightAttr(signature, midnightString(p.name(), line), line)
	}

	name := &ast.FuncName{Func: signature}
	if p.branch(":") {
		name = &ast.FuncName{Receiver: signature, Method: p.name()}
	}

	return midnightLine(&ast.FuncDefStmt{Name: name, Func: p.functionBody(line)}, line)
}

/* }}} */

/* Expressions {{{ */

func (p *midnightParser) expressionList() []ast.Expr {
	exprs := []ast.Expr{}
	p.list(func() { exprs = append(exprs, p.expression(1)) })
	return exprs
}

func (p *midnightParser) terminalExpression() ast.Expr {
	line := p.cur.Line

	switch p.cur.Type {
	case midnightEOF:
		p.throwEOF("unexpected eof (expected expression)")
	case midnightNumber:
		return midnightLine(&ast.NumberExpr{Value: p.consume()}, line)
	case midnightSingleQuote, midnightDoubleQuote, midnightBlockString:
		return p.indexChain(p.stringExpr()).expr
	case midnightMarkup:
		if p.cur.Value == "<" {
			return p.markupElement()
		}
	}

	if p.cur.Type == midnightWord || p.cur.Type == midnightSymbol {
		switch p.cur.Value {
		case "true":
			p.consume()
			return midnightLine(&ast.TrueExpr{}, line)
		case "false":
			p.consume()
			return midnightLine(&ast.FalseExpr{}, line)
		case "nil":
			p.consume()
			return midnightLine(&ast.NilExpr{}, line)
		case "...":
			if !p.isVarargs {
				p.throw(line, "cannot use '...' outside a vararg function")
			}
			p.consume()
			return midnightLine(&ast.Comma3Expr{}, line)
		}
	}

	if p.cur.Type == midnightWord && p.is("if") {
		return p.ifExpression()
	}
	if p.isMatchExpression() {
		return p.matchExpression()
	}

	isArrowFunction := p.isArrow(p.index + 1)

	if _, ok := midnightSurroundEnds[p.cur.Value]; ok && !isArrowFunction && p.cur.Type == midnightSymbol {
		_, pastIndex := p.lookPastSurround(p.index)
		isArrowFunction = p.isArrow(pastIndex)
	}

	switch {
	case isArrowFunction:
		return p.arrowFunction()
	case p.is("{"):
		return p.tableConstructor()
	case p.is("("):
		return p.indexChain(p.parenthesizedExpression()).expr
	}

	return p.indexChain(p.resolve(p.name(), line)).expr
}

func (p *midnightParser) parenthesizedExpression() ast.Expr {
	p.expect("(")
	expr := p.expression(1)
	p.expect(")")

	// Parentheses truncate multiple values to one.
	switch ex := expr.(type) {
	case *ast.FuncCallExpr:
		ex.AdjustRet = true
	case *ast.Comma3Expr:
		ex.AdjustRet = true
	}

	return expr
}

func (p *midnightParser) unopExpression() ast.Expr {
	line := p.cur.Line
	op := p.consume()
	operand := p.expression(midnightUnopPrec + 1)

	switch op {
	case "!":
		return midnightLine(&ast.UnaryNotOpExpr{Expr: operand}, line)
	case "-":
		return midnightLine(&ast.UnaryMinusOpExpr{Expr: operand}, line)
	case "#":
		return midnightLine(&ast.UnaryLenOpExpr{Expr: operand}, line)
	}

	return midnightLibCall(midnightBitlib, "bnot", []ast.Expr{operand}, line)
}

func (p *midnightParser) expression(minPrec int) ast.Expr {
	if p.cur.Type == midnightEOF {
		p.throwEOF("unexpected eof (expected expression)")
	}

	var expr ast.Expr
	switch {
	case p.is("-"), p.is("#"), p.is("!"), p.is("~"):
		expr = p.unopExpression()
	default:
		expr = p.terminalExpression()
	}

	for p.cur.Type == midnightSymbol {
		op, ok := midnightBinops[p.cur.Value]
		if !ok || op.prec < minPrec {
			break
		}

		line := p.cur.Line
		token := p.consume()

		if token == "~" && p.is("=") {
			p.throw(line, "unexpected token '~=', did you mean '!='?")
		}

		if op.rightAssoc {
			expr = p.binop(token, line, expr, p.expression(op.prec))
		} else {
			expr = p.binop(token, line, expr, p.expression(op.prec+1))
		}
	}

	return expr
}

/* }}} */

/* Blocks {{{ */

// block parses statements until a closing brace (not consumed).
func (p *midnightParser) block() []ast.Stmt {
	p.blockDepth++
	p.pushScope()

	stmts := []ast.Stmt{}
	for !p.is("}") {
		stmts = append(stmts, p.statement()...)
	}

	p.popScope()
	p.blockDepth--
	return stmts
}

func (p *midnightParser) braceBlock() []ast.Stmt {
	p.expect("{")
	stmts := p.block()
	p.expect("}")
	return stmts
}

func (p *midnightParser) loopBlock() []ast.Stmt {
	oldLoop := p.loop
	p.loop = &midnightLoop{continueLabel: p.newTmpName()}

	stmts := p.braceBlock()
	if p.loop.hasContinue {
		stmts = append(stmts, midnightLine(&ast.LabelStmt{Name: p.loop.continueLabel}, p.prevLine()))
	}

	p.loop = oldLoop
	return stmts
}

func (p *midnightParser) forLoop() ast.Stmt {
	line := p.cur.Line
	p.consume() // for

	next := p.tokens[p.index+1]
	if isMidnightSyntax(next, "=") {
		name := p.name()
		p.consume() // =

		exprsLine := p.cur.Line
		exprs := p.expressionList()
		if len(exprs) != 2 && len(exprs) != 3 {
			p.throw(exprsLine, "invalid numeric for, expected 2-3 expressions, got %d", len(exprs))
		}

		p.pushScope()
		p.declare(name, midnightScopeLocal)
		stmts := p.loopBlock()
		p.popScope()

		stmt := &ast.NumberForStmt{Name: name, Init: exprs[0], Limit: exprs[1], Stmts: stmts}
		if len(exprs) == 3 {
			stmt.Step = exprs[2]
		}
		return p.closed(midnightLine(stmt, line))
	}

	names := []string{}
	declared := []string{}
	prelude := []ast.Stmt{}

	p.list(func() {
		varLine := p.cur.Line
		name, destructure := p.variable(midnightScopeLocal)
		if destructure != nil {
			names = append(names, destructure.tmpName)
			declared = append(declared, destructure.names...)
			prelude = append(prelude, midnightLocal(destructure.names, nil, varLine))
			prelude = append(prelude, destructure.stmts...)
		} else {
			names = append(names, name)
			declared = append(declared, name)
		}
	})

	p.expect("in")
	exprs := p.expressionList()

	p.pushScope()
	for _, name := range declared {
		p.declare(name, midnightScopeLocal)
	}
	stmts := append(prelude, p.loopBlock()...)
	p.popScope()

	return p.closed(midnightLine(&ast.GenericForStmt{Names: names, Exprs: exprs, Stmts: stmts}, line))
}

func (p *midnightParser) ifElse() ast.Stmt {
	line := p.cur.Line
	p.consume() // if

	root := midnightLine(&ast.IfStmt{Condition: p.expression(1), Then: p.braceBlock(), Else: []ast.Stmt{}}, line)
	last := root

	for p.is("elseif") {
		elseifLine := p.cur.Line
		p.consume()
		elseif := midnightLine(&ast.IfStmt{Condition: p.expression(1), Then: p.braceBlock(), Else: []ast.Stmt{}}, elseifLine)
		last.Else = []ast.Stmt{elseif}
		last = elseif
	}

	if p.branch("else") {
		if p.is("if") {
			p.throw(p.cur.Line, "unexpected tokens 'else if', did you mean 'elseif'?")
		}
		last.Else = p.braceBlock()
	}

	return p.closed(root)
}

// isMatchStatement checks for a match statement. `match` is a
// contextual keyword: it is an ordinary name everywhere else (ex.
// `s:match(p)`), and at the start of a statement when followed by anything
// that continues a name (ex. `match = 1`, `match.x = 1`, `match(x)`).
func (p *midnightParser) isMatchStatement() bool {
	if p.cur.Type != midnightWord || !p.is("match") {
		return false
	}

	next := p.tokens[p.index+1]
	if next.Type != midnightSymbol {
		return true
	}

	switch next.Value {
	case "=", ",", ".", "[", ":":
		return false
	case "(":
		// `match(x)` is a call, `match (x) {` is a match statement.
		past, _ := p.lookPastSurround(p.index + 1)
		return isMidnightSyntax(past, "{")
	}

	// `match += 1` (but `match -x {` is a match statement)
	return !midnightAssignmentOperators[next.Value] || !isMidnightSyntax(p.tokens[p.index+2], "=")
}

// matchStatement compiles a match statement into an if chain:
//
//	do
//	  local subject = <subject>
//	  if subject == a or subject == b then ... elseif ... else ... end
//	end
func (p *midnightParser) matchStatement() ast.Stmt {
	line := p.cur.Line
	p.consume() // match

	subjectName := p.newTmpName()
	subject := midnightIdent(subjectName, line)
	stmts := []ast.Stmt{midnightLocal([]string{subjectName}, []ast.Expr{p.expression(1)}, line)}

	var root, last *ast.IfStmt
	var fallback []ast.Stmt
	hasFallback := false

	p.expect("{")
	for !p.is("}") {
		if p.cur.Type == midnightEOF {
			p.throwEOF("unexpected eof, missing '}'")
		}
		if hasFallback {
			p.throw(p.cur.Line, "unreachable match arm (after '_')")
		}

		armLine := p.cur.Line
		condition := p.matchArmCondition(subject)
		body := p.braceBlock()
		p.branch(",")

		if condition == nil {
			fallback, hasFallback = body, true
			continue
		}

		arm := midnightLine(&ast.IfStmt{Condition: condition, Then: body, Else: []ast.Stmt{}}, armLine)
		arm.SetLastLine(p.prevLine())
		if root == nil {
			root = arm
		} else {
			last.Else = []ast.Stmt{arm}
		}
		last = arm
	}
	p.expect("}")

	switch {
	case root != nil:
		if hasFallback {
			last.Else = fallback
		}
		stmts = append(stmts, p.closed(root))
	case hasFallback:
		stmts = append(stmts, p.closed(midnightLine(&ast.DoBlockStmt{Stmts: fallback}, line)))
	}

	return p.closed(midnightLine(&ast.DoBlockStmt{Stmts: stmts}, line))
}

// matchArmCondition parses `pattern {'|' pattern} ['if' guard] '=>'`, returning
// nil for a catch all arm (`_ =>`).
func (p *midnightParser) matchArmCondition(subject ast.Expr) ast.Expr {
	line := p.cur.Line

	oldArmArrowIndex := p.armArrowIndex
	p.armArrowIndex = p.findArmArrow()
	defer func() { p.armArrowIndex = oldArmArrowIndex }()

	var condition ast.Expr
	if p.is("_") && p.cur.Type == midnightWord {
		p.consume()
	} else {
		for {
			// Patterns stop before `|` (bitwise or), so `|` separates them.
			pattern := midnightLine(&ast.RelationalOpExpr{Operator: "==", Lhs: subject, Rhs: p.expression(midnightBinops["|"].prec + 1)}, line)
			if condition == nil {
				condition = pattern
			} else {
				condition = p.binop("||", line, condition, pattern)
			}
			if !p.branch("|") {
				break
			}
		}
	}

	if p.branch("if") {
		guard := p.expression(1)
		if condition == nil {
			condition = guard
		} else {
			condition = p.binop("&&", line, condition, guard)
		}
	}

	p.expect("=>")
	return condition
}

// findArmArrow finds the `=>` ending the current match arm's patterns (outside
// of any surrounds), or -1 if there is none.
func (p *midnightParser) findArmArrow() int {
	for i := p.index; ; {
		token := p.tokens[i]
		switch {
		case token.Type == midnightEOF:
			return -1
		case token.Type != midnightSymbol:
			i++
		case token.Value == "=>":
			return i
		case token.Value == "{", token.Value == "}":
			return -1 // arm body (or end of match), so the arrow is missing
		case midnightSurroundEnds[token.Value] != "":
			_, i = p.lookPastSurround(i)
		default:
			i++
		}
	}
}

/* }}} */

/* Assignments {{{ */

func (p *midnightParser) assignmentChain() *midnightChain {
	if p.is("(") {
		chain := p.indexChain(p.parenthesizedExpression())
		if chain.chainLen == 0 {
			if p.cur.Type == midnightEOF {
				p.throwEOF("unexpected eof")
			}
			p.throw(p.cur.Line, "unexpected token '%s'", p.cur.Value)
		}
		return chain
	}

	line := p.cur.Line
	return p.indexChain(p.resolve(p.name(), line))
}

var midnightAssignmentOperators = map[string]bool{
	"||": true, "&&": true, "..": true, "+": true, "-": true, "*": true,
	"/": true, "//": true, "%": true, "^": true, "|": true, "~": true,
	"&": true, "<<": true, ">>": true,
}

// operatorAssignment compiles `id op= value`. Index chains are only evaluated
// once (ex. `a[f()] += 1` only calls `f` once).
func (p *midnightParser) operatorAssignment(id *midnightChain, value ast.Expr, op string, line int) []ast.Stmt {
	if id.chainLen == 0 {
		return []ast.Stmt{midnightAssign(id.expr, p.binop(op, line, id.expr, value), line)}
	}

	stmts := []ast.Stmt{}
	base, index := id.finalBase, id.finalIndex

	if _, ok := base.(*ast.IdentExpr); !ok {
		tmpName := p.newTmpName()
		stmts = append(stmts, midnightLocal([]string{tmpName}, []ast.Expr{base}, line))
		base = midnightIdent(tmpName, line)
	}

	if _, ok := index.(ast.ConstExpr); !ok {
		tmpName := p.newTmpName()
		stmts = append(stmts, midnightLocal([]string{tmpName}, []ast.Expr{index}, line))
		index = midnightIdent(tmpName, line)
	}

	current := midnightAttr(base, index, line)
	return append(stmts, midnightAssign(midnightAttr(base, index, line), p.binop(op, line, current, value), line))
}

func (p *midnightParser) variableAssignment(first *midnightChain, line int) []ast.Stmt {
	ids := []*midnightChain{first}

	for p.branch(",") {
		idLine := p.cur.Line
		id := p.assignmentChain()
		if id.isCall {
			p.throw(idLine, "cannot assign value to function call")
		}
		ids = append(ids, id)
	}

	opLine := p.cur.Line
	op := ""
	if p.cur.Type == midnightSymbol && midnightAssignmentOperators[p.cur.Value] {
		op = p.consume()
	}

	p.expect("=")
	exprs := p.expressionList()

	if op == "" {
		lhs := make([]ast.Expr, len(ids))
		for i, id := range ids {
			lhs[i] = id.expr
		}
		assign := midnightLine(&ast.AssignStmt{Lhs: lhs, Rhs: exprs}, line)
		if hoisted := p.hoistCond(exprs, line); hoisted != nil {
			// scope the temporary local
			return []ast.Stmt{midnightLine(&ast.DoBlockStmt{Stmts: append(hoisted, assign)}, line)}
		}
		return []ast.Stmt{assign}
	}

	var stmts []ast.Stmt

	if len(ids) == 1 {
		stmts = p.operatorAssignment(ids[0], exprs[0], op, opLine)
		if len(stmts) == 1 {
			return stmts
		}
	} else {
		tmpNames := make([]string, len(ids))
		for i := range ids {
			tmpNames[i] = p.newTmpName()
		}

		stmts = []ast.Stmt{midnightLocal(tmpNames, exprs, line)}
		for i, id := range ids {
			stmts = append(stmts, p.operatorAssignment(id, midnightIdent(tmpNames[i], opLine), op, opLine)...)
		}
	}

	// Scope temporary locals
	return []ast.Stmt{midnightLine(&ast.DoBlockStmt{Stmts: stmts}, line)}
}

/* }}} */

/* Declarations {{{ */

func (p *midnightParser) variableDeclaration(scope string, line int) []ast.Stmt {
	names := []string{}
	destructures := []*midnightDestructure{}

	p.list(func() {
		name, destructure := p.variable(scope)
		if destructure != nil {
			destructures = append(destructures, destructure)
			names = append(names, destructure.tmpName)
			for _, n := range destructure.names {
				p.declare(n, scope)
			}
		} else {
			names = append(names, name)
			p.declare(name, scope)
		}
	})

	if len(destructures) > 0 {
		p.expect("=")
	} else if scope == midnightScopeLocal && !p.is("=") {
		return []ast.Stmt{midnightLocal(names, nil, line)}
	} else if !p.branch("=") {
		return nil // nothing to do
	}

	exprs := p.expressionList()
	stmts := append([]ast.Stmt{}, p.hoistCond(exprs, line)...)

	if len(destructures) > 0 {
		declared := []string{}
		for _, d := range destructures {
			if scope == midnightScopeLocal {
				declared = append(declared, d.names...)
			} else {
				declared = append(declared, d.tmpName)
			}
		}
		stmts = append(stmts, midnightLocal(declared, nil, line))
	}

	if scope == midnightScopeLocal {
		stmts = append(stmts, midnightLocal(names, exprs, line))
	} else {
		lhs := make([]ast.Expr, len(names))
		for i, name := range names {
			lhs[i] = p.target(name, scope, line)
		}
		for _, d := range destructures {
			for i, name := range names {
				if name == d.tmpName {
					lhs[i] = midnightIdent(name, line)
				}
			}
		}
		stmts = append(stmts, midnightLine(&ast.AssignStmt{Lhs: lhs, Rhs: exprs}, line))
	}

	for _, d := range destructures {
		stmts = append(stmts, d.stmts...)
	}

	return stmts
}

/* }}} */

/* Statements {{{ */

func (p *midnightParser) statement() []ast.Stmt {
	line := p.cur.Line
	var stmts []ast.Stmt

	switch {
	case p.cur.Type == midnightEOF:
		p.throwEOF("unexpected eof")
	case p.is("break"):
		p.consume()
		if p.loop == nil {
			p.throw(line, "cannot use 'break' outside of loop")
		}
		stmts = []ast.Stmt{midnightLine(&ast.BreakStmt{}, line)}
	case p.is("continue"):
		p.consume()
		if p.loop == nil {
			p.throw(line, "cannot use 'continue' outside of loop")
		}
		p.loop.hasContinue = true
		stmts = []ast.Stmt{midnightLine(&ast.GotoStmt{Label: p.loop.continueLabel}, line)}
	case p.is("goto"):
		p.consume()
		stmts = []ast.Stmt{midnightLine(&ast.GotoStmt{Label: p.name()}, line)}
	case p.is("::"):
		p.consume()
		label := p.name()
		p.expect("::")
		stmts = []ast.Stmt{midnightLine(&ast.LabelStmt{Name: label}, line)}
	case p.is("do"):
		p.consume()
		stmts = []ast.Stmt{p.closed(midnightLine(&ast.DoBlockStmt{Stmts: p.braceBlock()}, line))}
	case p.is("if"):
		stmts = []ast.Stmt{p.ifElse()}
	case p.isMatchStatement():
		stmts = []ast.Stmt{p.matchStatement()}
	case p.is("for"):
		stmts = []ast.Stmt{p.forLoop()}
	case p.is("while"):
		p.consume()
		condition := p.expression(1)
		stmts = []ast.Stmt{p.closed(midnightLine(&ast.WhileStmt{Condition: condition, Stmts: p.loopBlock()}, line))}
	case p.is("repeat"):
		p.consume()
		body := p.loopBlock()
		p.expect("until")
		stmts = []ast.Stmt{midnightLine(&ast.RepeatStmt{Stmts: body, Condition: p.expression(1)}, line)}
	case p.is("return"):
		stmts = []ast.Stmt{p.returnStmt()}
	case p.is("function"):
		stmts = []ast.Stmt{p.functionDeclaration("")}
	case p.is("local"), p.is("global"), p.is("module"):
		scope := p.consume()

		if scope == midnightScopeModule {
			if p.blockDepth > 1 {
				p.throw(line, "module declarations must appear at the top level")
			}
			p.hasModuleDecls = true
		}

		if p.is("function") {
			stmts = []ast.Stmt{p.functionDeclaration(scope)}
		} else {
			stmts = p.variableDeclaration(scope, line)
		}
	default:
		chain := p.assignmentChain()
		if chain.isCall {
			stmts = []ast.Stmt{midnightLine(&ast.FuncCallStmt{Expr: chain.expr}, line)}
		} else {
			stmts = p.variableAssignment(chain, line)
		}
	}

	p.branch(";")
	return stmts
}

func (p *midnightParser) module() []ast.Stmt {
	stmts := []ast.Stmt{}
	for p.cur.Type != midnightEOF {
		stmts = append(stmts, p.statement()...)
	}

	if p.hasModuleDecls {
		if p.moduleReturnLine != 0 {
			p.throw(p.moduleReturnLine, "cannot use 'return' w/ 'module' declarations")
		}

		line := p.cur.Line
		moduleTable := midnightLine(&ast.TableExpr{Fields: []*ast.Field{}}, 1)
		stmts = append([]ast.Stmt{midnightLocal([]string{midnightModuleName}, []ast.Expr{moduleTable}, 1)}, stmts...)
		stmts = append(stmts, midnightLine(&ast.ReturnStmt{Exprs: []ast.Expr{midnightIdent(midnightModuleName, line)}}, line))
	}

	if p.markupRuntime != "" {
		stmts = append([]ast.Stmt{p.markupRuntimeImport()}, stmts...)
	}

	return stmts
}

/* }}} */
