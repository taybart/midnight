// Midnight parser: assignments, declarations, and statements (see
// midnight_parser.go).

package parse

import (
	"github.com/taybart/midnight/ast"
)

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
	isModule := p.isModuleAccess()
	name := ""
	if !isModule && p.cur.Type == midnightWord {
		name = p.cur.Value
	}
	chain := p.indexChain(p.nameExpr(line))
	switch {
	case chain.isCall:
	case !isModule && chain.chainLen == 0:
		chain.name = name
	case isModule && chain.chainLen == 1:
		if key, ok := chain.finalIndex.(*ast.StringExpr); ok {
			chain.name, chain.isModule = key.Value, true
		}
	}
	return chain
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
	p.checkAssignable(first.name, first.isModule, line)

	for p.branch(",") {
		idLine := p.cur.Line
		id := p.assignmentChain()
		if id.isCall {
			p.throw(idLine, "cannot assign value to function call")
		}
		p.checkAssignable(id.name, id.isModule, idLine)
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

// variableDeclaration parses the names and values of a declaration, after its
// scope (and `const`, when isConst).
func (p *midnightParser) variableDeclaration(scope string, line int, isConst bool) []ast.Stmt {
	names := []string{}
	destructures := []*midnightDestructure{}
	declared := []string{}
	firstDecl := 0
	if p.syms != nil {
		firstDecl = len(p.syms.Decls)
	}

	p.list(func() {
		name, destructure := p.variable(scope)
		if destructure != nil {
			destructures = append(destructures, destructure)
			names = append(names, destructure.tmpName)
			destructure.declareAll(p, scope)
			declared = append(declared, destructure.names...)
		} else {
			names = append(names, name)
			p.declareAt(name, scope, p.lastName)
			declared = append(declared, name)
		}
	})

	// (the values can't refer to the names, ex. the x in `local x = x + 1`
	// is the outer x)
	defer p.holdDecls(declared)()

	if isConst {
		if !p.is("=") {
			p.throw(line, "const '%s' needs a value (ex. const %s = ...)", declared[0], declared[0])
		}
		for _, n := range declared {
			p.declareConst(n, scope, line)
		}
	}

	if len(destructures) > 0 {
		p.expect("=")
	} else if scope == midnightScopeLocal && !p.is("=") {
		return []ast.Stmt{midnightLocal(names, nil, line)}
	} else if !p.branch("=") {
		return nil // nothing to do
	}

	exprs := p.expressionList()
	p.markRequires(firstDecl, names, destructures, exprs)
	if isConst && len(destructures) == 0 {
		for i, expr := range exprs {
			exprs[i] = midnightFrozen(expr)
		}
	}
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
		condition := p.headerExpression()
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
	case p.isClassDeclaration():
		stmts = p.classDeclaration("")
	case p.is("local"), p.is("global"), p.is("module") && !p.isModuleAccess():
		scope := p.consume()

		if scope == midnightScopeModule {
			if p.blockDepth > 1 {
				p.throw(line, "module declarations must appear at the top level")
			}
			p.hasModuleDecls = true
		}

		if p.is("function") {
			stmts = []ast.Stmt{p.functionDeclaration(scope)}
		} else if p.isClassDeclaration() {
			stmts = p.classDeclaration(scope)
		} else if p.isConstDeclaration() {
			if scope != midnightScopeModule {
				p.throw(line, "cannot use '%s const': use 'const' (or 'module const')", scope)
			}
			p.consume()
			stmts = p.variableDeclaration(scope, line, true)
		} else {
			stmts = p.variableDeclaration(scope, line, false)
		}
	case p.isConstDeclaration():
		p.consume()
		stmts = p.variableDeclaration(midnightScopeLocal, line, true)
	default:
		chain := p.assignmentChain()
		if chain.isCall && p.isCatch() {
			stmts = []ast.Stmt{p.catchStatement(chain.expr)}
		} else if chain.isCall {
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
	p.checkExitingCatches()

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
