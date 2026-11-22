// Helpers for building the (Lua) AST Midnight is lowered to.

package parse

import (
	"github.com/taybart/midnight/ast"
)

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

// midnightFrozen wraps a table constructor (`const t = { ... }`) in
// `table.freeze(...)`, and the constructors that are its values, so a const
// table literal is read-only all the way down. Anything else is returned as
// is: only a table made right there is safe to freeze (`const m = require(...)`
// would freeze a table other code has too).
func midnightFrozen(expr ast.Expr) ast.Expr {
	table, ok := expr.(*ast.TableExpr)
	if !ok {
		return expr
	}
	for _, field := range table.Fields {
		field.Value = midnightFrozen(field.Value)
	}
	return midnightLibCall("table", "freeze", []ast.Expr{table}, table.Line())
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
