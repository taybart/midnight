// Midnight parser: returns and functions (see midnight_parser.go).

package parse

import (
	"github.com/taybart/midnight/ast"
)

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
				p.declareAt(name, midnightScopeLocal, p.lastName)
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
			destructure.declareAll(p, midnightScopeLocal)
		} else {
			p.declareAt(name, midnightScopeLocal, p.lastName)
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
			destructure.declareAll(p, midnightScopeLocal)
			prelude = append(prelude, midnightLocal(destructure.names, nil, line))
			prelude = append(prelude, destructure.stmts...)
		} else {
			p.declareAt(name, midnightScopeLocal, p.lastName)
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
	baseTok := p.lastName

	if scope != "" && (p.is(".") || p.is(":")) {
		// Lua does not allow scope for table values (ex. `local function a.b()`)
		p.throw(p.cur.Line, "cannot use scopes for table values")
	}

	if scope == midnightScopeLocal {
		p.declareAt(baseName, scope, baseTok)
		return midnightLocal([]string{baseName}, []ast.Expr{p.functionBody(line)}, line)
	}

	var signature ast.Expr
	if scope != "" {
		p.declareAt(baseName, scope, baseTok)
		signature = p.target(baseName, scope, line)
	} else {
		if !p.is(".") && !p.is(":") {
			p.checkAssignable(baseName, false, line) // (`function x()` assigns x)
		}
		p.ref(baseName, baseTok, 0)
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
