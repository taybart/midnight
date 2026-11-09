package parse

import (
	"github.com/taybart/midnight/ast"
)

// `if` and `match` expressions: an `if` / `match` where an expression is
// expected has a value, the value of the branch that's taken:
//
//	local label = if user.admin { user.title } else { 'member' }
//	local icon = match kind { 'dir' => { '📁' } _ => { '📄' } }
//
// Each branch is one expression (truncated to one value, like parentheses),
// and the fallback (`else` / `_ =>`) is required, so a missing case is never a
// silent nil.
//
// They're lowered to an if chain that hands the value of the branch taken to
// a target (see lowerCond). As the (first) value of a declaration or
// assignment, or of a return (including an arrow function's expression body),
// the target is a local or the return, so it costs nothing:
//
//	local x = if c { a } else { b }
//	local tmp; if c then tmp = a else tmp = b end; local x = tmp
//
// (through a temporary, so `local x = if c { x } ...` still means the outer
// x.) Anywhere else, the if chain is in a function that's called right away:
//
//	f(if c { a } else { b })
//	f((function() if c then return a else return b end end)())

// midnightCond is a parsed `if` / `match` expression.
type midnightCond struct {
	line int
	// match: `local <subjectName> = <subject>`, before the arms
	subjectName string
	subject     ast.Expr
	arms        []midnightCondArm
	fallback    ast.Expr
}

type midnightCondArm struct {
	line      int
	condition ast.Expr
	value     ast.Expr
}

// condValue parses a branch's `{ expression }`.
func (p *midnightParser) condValue() ast.Expr {
	p.expect("{")
	if p.is("}") {
		p.throw(p.cur.Line, "expected an expression (the branch's value), got '}'")
	}
	if p.cur.Type == midnightWord && midnightKeywords[p.cur.Value] && !p.is("if") {
		p.throw(p.cur.Line, "unexpected '%s': a branch of an if / match expression is one expression, not statements (use an if statement)", p.cur.Value)
	}
	value := midnightSingle(p.expression(1))
	if !p.is("}") {
		p.throw(p.cur.Line, "expected '}' after the branch's value, got '%s' (a branch of an if / match expression is one expression)", p.cur.Value)
	}
	p.consume()
	return value
}

// midnightSingle truncates an expression to one value (like parentheses).
func midnightSingle(expr ast.Expr) ast.Expr {
	switch ex := expr.(type) {
	case *ast.FuncCallExpr:
		ex.AdjustRet = true
	case *ast.Comma3Expr:
		ex.AdjustRet = true
	}
	return expr
}

// ifExpression parses `if c { a } elseif d { b } else { c }`.
func (p *midnightParser) ifExpression() ast.Expr {
	cond := &midnightCond{line: p.cur.Line}
	p.consume() // if

	for {
		armLine := p.cur.Line
		condition := p.expression(1)
		cond.arms = append(cond.arms, midnightCondArm{line: armLine, condition: condition, value: p.condValue()})
		if !p.branch("elseif") {
			break
		}
	}

	if !p.is("else") {
		p.throw(p.cur.Line, "an if expression needs an 'else' (its value when nothing matches)")
	}
	p.consume()
	if p.is("if") {
		p.throw(p.cur.Line, "unexpected tokens 'else if', did you mean 'elseif'?")
	}
	cond.fallback = p.condValue()
	return p.condExpr(cond)
}

// isMatchExpression checks for a match expression (where an expression is
// expected). `match` is a name everywhere else, so it's only a match when it
// can't be one: the subject starts on the same line (a word, number, string,
// `(`, or a unary operator), and is followed by `{` and an arm (`... =>`). So
// `match - 1`, `match + x`, and `local y = match` (w/ the next statement on
// the next line) are still names, and `match #rows { 0 => ... }` is a match.
func (p *midnightParser) isMatchExpression() bool {
	if p.cur.Type != midnightWord || !p.is("match") {
		return false
	}

	next := p.tokens[p.index+1]
	if next.Line != p.cur.Line {
		return false
	}
	switch next.Type {
	case midnightWord:
		if midnightKeywords[next.Value] {
			return false
		}
	case midnightNumber, midnightSingleQuote, midnightDoubleQuote, midnightBlockString:
	default:
		switch {
		case isMidnightSyntax(next, "("), isMidnightSyntax(next, "#"), isMidnightSyntax(next, "!"),
			isMidnightSyntax(next, "-"), isMidnightSyntax(next, "~"):
		default:
			return false
		}
	}

	// past the subject to the arms' `{`
	i := p.index + 1
	for {
		token := p.tokens[i]
		switch {
		case token.Type == midnightEOF:
			return false
		case token.Type != midnightSymbol:
			i++
			continue
		case token.Value == "{":
			return p.isMatchArm(i + 1)
		case token.Value == "}" || token.Value == ";" || token.Value == "=" || token.Value == ",":
			return false
		case midnightSurroundEnds[token.Value] != "":
			_, i = p.lookPastSurround(i)
		default:
			i++
		}
	}
}

// isMatchArm checks for a match arm's `=>` from tokens[i] (before any `{` /
// `}`, outside of surrounds).
func (p *midnightParser) isMatchArm(i int) bool {
	for {
		token := p.tokens[i]
		switch {
		case token.Type == midnightEOF:
			return false
		case token.Type != midnightSymbol:
			i++
		case token.Value == "=>":
			return true
		case token.Value == "{", token.Value == "}":
			return false
		case midnightSurroundEnds[token.Value] != "":
			_, i = p.lookPastSurround(i)
		default:
			i++
		}
	}
}

// matchExpression parses `match s { a | b => { x } _ if g => { y } _ => { z } }`.
func (p *midnightParser) matchExpression() ast.Expr {
	cond := &midnightCond{line: p.cur.Line}
	p.consume() // match

	cond.subjectName = p.newTmpName()
	cond.subject = p.expression(1)
	subject := midnightIdent(cond.subjectName, cond.line)

	p.expect("{")
	hasFallback := false
	for !p.is("}") {
		if p.cur.Type == midnightEOF {
			p.throwEOF("unexpected eof, missing '}'")
		}
		if hasFallback {
			p.throw(p.cur.Line, "unreachable match arm (after '_')")
		}

		armLine := p.cur.Line
		condition := p.matchArmCondition(subject)
		value := p.condValue()
		p.branch(",")

		if condition == nil {
			cond.fallback, hasFallback = value, true
			continue
		}
		cond.arms = append(cond.arms, midnightCondArm{line: armLine, condition: condition, value: value})
	}
	if !hasFallback {
		p.throw(p.cur.Line, "a match expression needs a '_ =>' arm (its value when nothing matches)")
	}
	p.expect("}")
	return p.condExpr(cond)
}

// condExpr is the expression for a cond where it can't be lowered to
// statements: its if chain, in a function that's called right away. The cond
// is kept (see p.conds) so declarations, assignments, and returns can lower it
// to statements instead (see hoistCond).
func (p *midnightParser) condExpr(cond *midnightCond) ast.Expr {
	line := cond.line
	parList := &ast.ParList{Names: []string{}, HasVargs: p.isVarargs}
	fn := midnightLine(&ast.FunctionExpr{ParList: parList, Stmts: p.lowerCond(cond, midnightCondReturn)}, line)
	fn.SetLastLine(p.prevLine())
	args := []ast.Expr{}
	if p.isVarargs { // so `...` in a branch is the enclosing function's
		args = append(args, midnightLine(&ast.Comma3Expr{}, line))
	}
	call := midnightCall(fn, args, line)
	call.AdjustRet = true
	if p.conds == nil {
		p.conds = map[ast.Expr]*midnightCond{}
	}
	p.conds[call] = cond
	return call
}

// midnightCondReturn is the target that returns the value.
func midnightCondReturn(value ast.Expr, line int) ast.Stmt {
	return midnightLine(&ast.ReturnStmt{Exprs: []ast.Expr{value}}, line)
}

// lowerCond lowers a cond to an if chain whose branches hand their value to
// target. A branch whose value is itself an if / match expression is lowered
// into the same target (so nesting doesn't add functions).
func (p *midnightParser) lowerCond(cond *midnightCond, target func(value ast.Expr, line int) ast.Stmt) []ast.Stmt {
	branch := func(value ast.Expr, line int) []ast.Stmt {
		if inner, ok := p.conds[value]; ok {
			return p.lowerCond(inner, target)
		}
		return []ast.Stmt{target(value, line)}
	}

	var stmts []ast.Stmt
	if len(cond.arms) == 0 { // `match s { _ => { x } }`
		stmts = branch(cond.fallback, cond.line)
	} else {
		var root, last *ast.IfStmt
		for _, arm := range cond.arms {
			stmt := midnightLine(&ast.IfStmt{Condition: arm.condition, Then: branch(arm.value, arm.line), Else: []ast.Stmt{}}, arm.line)
			if root == nil {
				root = stmt
			} else {
				last.Else = []ast.Stmt{stmt}
			}
			last = stmt
		}
		last.Else = branch(cond.fallback, cond.line)
		root.SetLastLine(p.prevLine())
		stmts = []ast.Stmt{root}
	}

	if cond.subject != nil {
		subject := midnightLocal([]string{cond.subjectName}, []ast.Expr{cond.subject}, cond.line)
		block := midnightLine(&ast.DoBlockStmt{Stmts: append([]ast.Stmt{subject}, stmts...)}, cond.line)
		block.SetLastLine(p.prevLine())
		return []ast.Stmt{block}
	}
	return stmts
}

// hoistCond lowers exprs[0] to statements when it's an if / match expression:
// it returns the statements that compute it into a local, and replaces it w/
// that local. (Only the first expression: hoisting a later one would change
// the order the expressions are evaluated in.)
func (p *midnightParser) hoistCond(exprs []ast.Expr, line int) []ast.Stmt {
	if len(exprs) == 0 {
		return nil
	}
	cond, ok := p.conds[exprs[0]]
	if !ok {
		return nil
	}
	tmp := p.newTmpName()
	stmts := []ast.Stmt{midnightLocal([]string{tmp}, nil, line)}
	stmts = append(stmts, p.lowerCond(cond, func(value ast.Expr, line int) ast.Stmt {
		return midnightAssign(midnightIdent(tmp, line), value, line)
	})...)
	exprs[0] = midnightIdent(tmp, line)
	return stmts
}

// returnCond lowers `return <if / match expression>` to an if chain w/ a
// return in each branch, or returns nil.
func (p *midnightParser) returnCond(exprs []ast.Expr, line int) ast.Stmt {
	if len(exprs) != 1 {
		return nil
	}
	cond, ok := p.conds[exprs[0]]
	if !ok {
		return nil
	}
	stmts := p.lowerCond(cond, midnightCondReturn)
	if len(stmts) == 1 {
		return stmts[0]
	}
	block := midnightLine(&ast.DoBlockStmt{Stmts: stmts}, line)
	block.SetLastLine(p.prevLine())
	return block
}
