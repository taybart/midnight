// Midnight parser: blocks, loops, if, and match (see midnight_parser.go).

package parse

import (
	"github.com/taybart/midnight/ast"
)

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
		nameTok := p.lastName
		p.consume() // =

		exprsLine := p.cur.Line
		var exprs []ast.Expr
		p.header(func() { exprs = p.expressionList() })
		if len(exprs) != 2 && len(exprs) != 3 {
			p.throw(exprsLine, "invalid numeric for, expected 2-3 expressions, got %d", len(exprs))
		}

		p.pushScope()
		p.declareAt(name, midnightScopeLocal, nameTok)
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
	declaredToks := []int{}
	prelude := []ast.Stmt{}

	p.list(func() {
		varLine := p.cur.Line
		name, destructure := p.variable(midnightScopeLocal)
		if destructure != nil {
			names = append(names, destructure.tmpName)
			declared = append(declared, destructure.names...)
			declaredToks = append(declaredToks, destructure.tokens...)
			prelude = append(prelude, midnightLocal(destructure.names, nil, varLine))
			prelude = append(prelude, destructure.stmts...)
		} else {
			names = append(names, name)
			declared = append(declared, name)
			declaredToks = append(declaredToks, p.lastName)
		}
	})

	p.expect("in")
	var exprs []ast.Expr
	p.header(func() { exprs = p.expressionList() })

	p.pushScope()
	for i, name := range declared {
		p.declareAt(name, midnightScopeLocal, declaredToks[i])
	}
	stmts := append(prelude, p.loopBlock()...)
	p.popScope()

	return p.closed(midnightLine(&ast.GenericForStmt{Names: names, Exprs: exprs, Stmts: stmts}, line))
}

func (p *midnightParser) ifElse() ast.Stmt {
	line := p.cur.Line
	p.consume() // if

	root := midnightLine(&ast.IfStmt{Condition: p.headerExpression(), Then: p.braceBlock(), Else: []ast.Stmt{}}, line)
	last := root

	for p.is("elseif") {
		elseifLine := p.cur.Line
		p.consume()
		elseif := midnightLine(&ast.IfStmt{Condition: p.headerExpression(), Then: p.braceBlock(), Else: []ast.Stmt{}}, elseifLine)
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
	stmts := []ast.Stmt{midnightLocal([]string{subjectName}, []ast.Expr{p.headerExpression()}, line)}

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
