package mngen

import (
	"bytes"
	"errors"
	"reflect"
	"strconv"
	"strings"

	"github.com/taybart/midnight/ast"
	"github.com/taybart/midnight/parse"
)

// verify checks that Midnight source parses to the same program as a Lua
// chunk. If not, it returns the line of the first Lua statement that differs.
// (lua is normalized in place, it's not used after.)
func verify(lua []ast.Stmt, midnight []byte, opts Options) (int, bool) {
	converted, err := parse.ParseMidnight(bytes.NewReader(midnight), "<convert>")
	if err != nil {
		return errorLine(err), false
	}

	normalize(reflect.ValueOf(lua), opts)
	normalize(reflect.ValueOf(converted), opts)

	for i, stmt := range lua {
		if i >= len(converted) || parse.Dump([]ast.Stmt{stmt}) != parse.Dump([]ast.Stmt{converted[i]}) {
			return stmt.Line(), false
		}
	}
	if len(converted) != len(lua) {
		return converted[len(lua)].Line(), false
	}
	return 0, true
}

// normalize rewrites the parts of a chunk that differ between the Lua and
// Midnight parsers w/o changing the program: number literals (`0x10` vs
// `16`), and the grouping of `..` chains (see the package docs).
func normalize(v reflect.Value, opts Options) {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			normalize(v.Elem(), opts)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}
		normalize(v.Elem(), opts)
		if expr, ok := v.Interface().(*ast.StringConcatOpExpr); ok && v.CanSet() {
			v.Set(reflect.ValueOf(rightConcat(expr, opts)))
		}
	case reflect.Struct:
		if n, ok := v.Addr().Interface().(*ast.NumberExpr); ok {
			n.Value = canonicalNumber(n.Value)
			return
		}
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				normalize(v.Field(i), opts)
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			normalize(v.Index(i), opts)
		}
	}
}

// rightConcat regroups a `..` chain to the right, like Lua: a .. (b .. c).
// W/ Options.Interp, the operands are normalized too (see interpOperands).
func rightConcat(expr *ast.StringConcatOpExpr, opts Options) ast.Expr {
	var operands []ast.Expr
	var flatten func(ast.Expr)
	flatten = func(e ast.Expr) {
		if c, ok := e.(*ast.StringConcatOpExpr); ok {
			flatten(c.Lhs)
			flatten(c.Rhs)
		} else {
			operands = append(operands, e)
		}
	}
	flatten(expr)
	if opts.Interp {
		operands = interpOperands(operands)
	}

	result := operands[len(operands)-1]
	for i := len(operands) - 2; i >= 0; i-- {
		result = &ast.StringConcatOpExpr{Lhs: operands[i], Rhs: result}
	}
	return result
}

func canonicalNumber(value string) string {
	lower := strings.ToLower(value)
	if hex, ok := strings.CutPrefix(lower, "0x"); ok {
		if n, err := strconv.ParseUint(hex, 16, 64); err == nil {
			return strconv.FormatFloat(float64(n), 'g', -1, 64)
		}
		return value
	}
	if n, err := strconv.ParseFloat(value, 64); err == nil {
		return strconv.FormatFloat(n, 'g', -1, 64)
	}
	return value
}

// errorLine returns the line of a parse error (or 0).
func errorLine(err error) int {
	var perr *parse.Error
	if errors.As(err, &perr) {
		return perr.Pos.Line
	}
	return 0
}

func errorMessage(err error) string {
	var perr *parse.Error
	if errors.As(err, &perr) {
		return perr.Message
	}
	return err.Error()
}
