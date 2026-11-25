package mngen

import "github.com/taybart/midnight/ast"

// Options are opt-in conversions. Unlike the default syntax translation, they
// can change what a program does in edge cases (see each one).
type Options struct {
	// Interp writes `..` chains that have a string literal as string
	// interpolation: `'id: ' .. id .. '!'` -> `"id: {id}!"`, and
	// `'n=' .. tostring(n)` -> `"n={n}"`. Interpolation calls tostring, so a
	// value `..` rejects (nil, a boolean, a table w/o __concat) becomes text
	// instead of an error, and __concat metamethods aren't called. Chains
	// that span lines are kept as `..`.
	Interp bool
}

// interp writes a `..` chain (its flattened operands, inside any parentheses
// concat wrote) as an interpolated string, if Options.Interp is set and it
// can be one. It reports whether it did; if not, nothing was written.
func (g *generator) interp(operands []ast.Expr) bool {
	if !g.opts.Interp {
		return false
	}
	line := operands[0].Line()
	text, values := false, false
	for _, operand := range operands {
		if operand.Line() != line {
			return false // (keep the source's line breaks and comments)
		}
		if s, ok := operand.(*ast.StringExpr); ok {
			text = text || s.Value != ""
		} else {
			values = true
		}
	}
	if !text || !values {
		return false
	}

	saved := g.save()
	g.write(`"`)
	for _, operand := range operands {
		if s, ok := operand.(*ast.StringExpr); ok {
			g.write(quoteBody(s.Value, '"'))
			continue
		}
		if arg, ok := tostringArg(operand); ok {
			operand = arg
		}
		g.write("{")
		g.expr(operand, 0)
		g.write("}")
	}
	g.write(`"`)

	// an operand that spans lines (ex. a function) or is a block string
	if g.line != line {
		g.restore(saved)
		return false
	}
	return true
}

// tostringArg returns x for `tostring(x)`.
func tostringArg(expr ast.Expr) (ast.Expr, bool) {
	call, ok := expr.(*ast.FuncCallExpr)
	if !ok || call.Receiver != nil || call.AdjustRet || len(call.Args) != 1 {
		return nil, false
	}
	if ident, ok := call.Func.(*ast.IdentExpr); ok && ident.Value == "tostring" {
		return call.Args[0], true
	}
	return nil, false
}

// generatorState is what interp restores when it backs out.
type generatorState struct {
	bufLen, line, stmtEnd, nextComment, errs int
	fresh                                    bool
	usedParens, usedBraces                   map[int]int
}

func (g *generator) save() generatorState {
	return generatorState{
		bufLen: len(g.buf), line: g.line, stmtEnd: g.stmtEnd, nextComment: g.nextComment,
		errs: len(g.errs), fresh: g.fresh,
		usedParens: copyCounts(g.usedParens), usedBraces: copyCounts(g.usedBraces),
	}
}

func (g *generator) restore(s generatorState) {
	g.buf = g.buf[:s.bufLen]
	g.line, g.stmtEnd, g.nextComment, g.fresh = s.line, s.stmtEnd, s.nextComment, s.fresh
	g.errs = g.errs[:s.errs]
	g.usedParens, g.usedBraces = s.usedParens, s.usedBraces
}

func copyCounts(m map[int]int) map[int]int {
	c := make(map[int]int, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

// interpOperands normalizes a flattened `..` chain for verify w/
// Options.Interp: `tostring(x)` is x (as an interpolation is), and adjacent
// string literals are merged (`'a' .. 'b' .. x` is written `"ab{x}"`).
func interpOperands(operands []ast.Expr) []ast.Expr {
	var out []ast.Expr
	for _, operand := range operands {
		if arg, ok := tostringArg(operand); ok {
			operand = arg
		}
		if s, ok := operand.(*ast.StringExpr); ok && len(out) > 0 {
			if prev, ok := out[len(out)-1].(*ast.StringExpr); ok {
				merged := *prev
				merged.Value += s.Value
				out[len(out)-1] = &merged
				continue
			}
		}
		out = append(out, operand)
	}
	return out
}

// quoteBody is s escaped for a string literal quoted w/ q (w/o the quotes).
func quoteBody(s string, q byte) string {
	quoted := quoteWith(s, q)
	return quoted[1 : len(quoted)-1]
}
