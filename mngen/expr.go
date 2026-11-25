package mngen

import (
	"fmt"

	"github.com/taybart/midnight/ast"
)

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

	if g.interp(operands) {
		return
	}

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
