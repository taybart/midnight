// Midnight parser: names, index chains, strings, tables, and expressions (see
// midnight_parser.go).

package parse

import (
	"strconv"

	"github.com/taybart/midnight/ast"
)

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
	p.lastName = p.index
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
		d.tokens = append(d.tokens, p.lastName)

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
		d.tokens = append(d.tokens, p.lastName)
		d.keys = append(d.keys, key)

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
			if chain.chainLen == 0 {
				p.field(p.index)
			}
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

	table := midnightLine(&ast.TableExpr{Fields: fields}, line)
	table.SetLastLine(p.prevLine()) // the closing brace
	return table
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

	return p.indexChain(p.nameExpr(line)).expr
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

	for minPrec <= midnightCatchPrec && p.isCatch() {
		expr = p.catchExpression(expr)
	}

	return expr
}

/* }}} */
