package parse

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/taybart/midnight/ast"
)

// markup experiment (see MidnightOptions.Markup). Elements are lowered to calls of the
// factory function (`h` by default), same as JSX's classic runtime:
//
//	<div class="card" id={id}>hi {name}</div>
//	h('div', { class = 'card', id = id }, 'hi ', name)
//
//	<Card {...props} title="x"/>   h(Card, <props merged left to right>)
//	<ui.Button/>                   h(ui.Button, nil)
//	<>a {b}</>                     h(nil, nil, 'a ', b)
//
// `h` is whatever `h` is in scope. When there isn't one, it's imported from
// the runtime library (`require('midnight.markup').h`), so components don't need any
// imports (like JSX's automatic runtime).
//
// The content of <script> and <style> is raw text, like HTML (see
// markupRawTextTags), so `<style>a { color: red }</style>` is css, not an
// expression.
//
// Lowercase tags (and tags w/ `-` or `:`) are strings, other tags are
// expressions. Attribute strings are midnight strings (so `class="btn {kind}"`
// interpolates), an attribute w/o a value is `true`, and text is cleaned up
// like JSX (lines are trimmed, whitespace only lines are dropped, and lines
// are joined w/ a single space).
//
// Markup is lexed as part of the token stream: a `<` followed by a name or `>`
// starts an element wherever an expression may start (ie. not after a token
// that ends a value, so `a < b` is still a comparison).

/* Lexer {{{ */

func isMarkupNameHead(c byte) bool { return isMidnightWordHead(c) || c == '@' || c == ':' }

func isMarkupNameBody(c byte) bool {
	return isMidnightWordBody(c) || c == '-' || c == '.' || c == ':'
}

// markupStart returns whether the `<` at the current position starts an element.
func (lx *midnightLexer) markupStart() bool {
	if c := lx.peek(1); !(isMidnightWordHead(c) || c == '>') {
		return false
	}
	if len(lx.tokens) == 0 {
		return true
	}

	// Elements can't follow a token that ends a value.
	prev := lx.tokens[len(lx.tokens)-1]
	switch prev.Type {
	case midnightWord:
		return midnightKeywords[prev.Value]
	case midnightNumber, midnightSingleQuote, midnightDoubleQuote, midnightBlockString:
		return false
	case midnightSymbol:
		switch prev.Value {
		case ")", "]", "}", "...":
			return false
		}
	case midnightMarkup:
		return prev.Value != ">" && prev.Value != "/>"
	}
	return true
}

// markupSpace skips whitespace and comments inside of a tag.
func (lx *midnightLexer) markupSpace() {
	for !lx.eof() {
		switch c := lx.peek(0); {
		case c == '\n':
			lx.line++
			lx.pos++
		case c == ' ' || c == '\t' || c == '\r':
			lx.pos++
		case c == '-' && lx.peek(1) == '-':
			lx.comment()
		default:
			return
		}
	}
}

func (lx *midnightLexer) markupName() {
	if !isMarkupNameHead(lx.peek(0)) {
		lx.markupUnexpected("name")
	}
	start := lx.pos
	lx.pos++
	for !lx.eof() && isMarkupNameBody(lx.peek(0)) {
		lx.pos++
	}
	lx.emit(midnightMarkupName, lx.text[start:lx.pos], lx.line)
}

func (lx *midnightLexer) markupUnexpected(expected string) {
	if lx.eof() {
		lx.throwEOF("unterminated markup element")
	}
	r, _ := utf8.DecodeRuneInString(lx.text[lx.pos:])
	lx.throw(lx.line, "unexpected '%c' in markup element (expected %s)", r, expected)
}

// markupElement lexes an element, starting at its `<`.
func (lx *midnightLexer) markupElement() {
	lx.emit(midnightMarkup, "<", lx.line)
	lx.pos++

	if lx.peek(0) == '>' { // fragment
		lx.emit(midnightMarkup, ">", lx.line)
		lx.pos++
		lx.markupChildren()
		return
	}

	lx.markupName()
	tag := lx.tokens[len(lx.tokens)-1].Value

	for {
		lx.markupSpace()

		switch c := lx.peek(0); {
		case c == '/' && lx.peek(1) == '>':
			lx.emit(midnightMarkup, "/>", lx.line)
			lx.pos += 2
			return
		case c == '>':
			lx.emit(midnightMarkup, ">", lx.line)
			lx.pos++
			if markupRawTextTags[tag] {
				lx.markupRawText(tag)
			}
			lx.markupChildren()
			return
		case c == '{': // spread
			lx.markupExpression()
		case isMarkupNameHead(c):
			lx.markupName()
			lx.markupSpace()
			if lx.peek(0) != '=' {
				continue
			}
			lx.emit(midnightMarkup, "=", lx.line)
			lx.pos++
			lx.markupSpace()
			switch lx.peek(0) {
			case '"':
				lx.doubleQuoteString()
			case '\'':
				lx.singleQuoteString()
			case '{':
				lx.markupExpression()
			default:
				lx.markupUnexpected("attribute value")
			}
		default:
			lx.markupUnexpected("attribute, '>', or '/>'")
		}
	}
}

// Like HTML, the content of these is text as is (no `{...}` expressions,
// whitespace cleanup, or entities), so css / js can be written directly.
var markupRawTextTags = map[string]bool{"script": true, "style": true}

// markupRawText lexes the content of a raw text element (see markupRawTextTags), up
// to its closing tag.
func (lx *midnightLexer) markupRawText(tag string) {
	start, startLine := lx.pos, lx.line
	end := strings.Index(lx.text[lx.pos:], "</"+tag)
	if end < 0 {
		lx.throwEOF("unterminated markup element '<%s>'", tag)
	}
	text := lx.text[start : start+end]
	lx.pos += end
	lx.line += strings.Count(text, "\n")
	if strings.TrimSpace(text) != "" {
		lx.emit(midnightMarkupText, text, startLine)
	}
}

// markupChildren lexes an element's children, up to and including its closing
// tag.
func (lx *midnightLexer) markupChildren() {
	start, startLine := lx.pos, lx.line

	flush := func() {
		if text := markupText(lx.text[start:lx.pos]); text != "" {
			lx.emit(midnightMarkupText, text, startLine)
		}
	}

	for {
		switch c := lx.peek(0); {
		case lx.eof():
			lx.throwEOF("unterminated markup element")
		case c == '<' && lx.peek(1) == '/':
			flush()
			lx.emit(midnightMarkup, "</", lx.line)
			lx.pos += 2
			lx.markupSpace()
			if lx.peek(0) != '>' {
				lx.markupName()
				lx.markupSpace()
			}
			if lx.peek(0) != '>' {
				lx.markupUnexpected("'>'")
			}
			lx.emit(midnightMarkup, ">", lx.line)
			lx.pos++
			return
		case c == '<':
			flush()
			lx.markupElement()
			start, startLine = lx.pos, lx.line
		case c == '{':
			flush()
			lx.markupExpression()
			start, startLine = lx.pos, lx.line
		default:
			if c == '\n' {
				lx.line++
			}
			lx.pos++
		}
	}
}

// markupExpression lexes a `{...}` expression (or spread) inside of an element.
func (lx *midnightLexer) markupExpression() {
	lx.emit(midnightMarkup, "{", lx.line)
	lx.pos++

	braceDepth := 0
	for lx.peek(0) != '}' || braceDepth > 0 {
		switch {
		case lx.eof():
			lx.throwEOF("unterminated markup expression")
		case lx.peek(0) == '{':
			braceDepth++
			lx.emit(midnightSymbol, "{", lx.line)
			lx.pos++
		case lx.peek(0) == '}':
			braceDepth--
			lx.emit(midnightSymbol, "}", lx.line)
			lx.pos++
		default:
			lx.token()
		}
	}

	lx.emit(midnightMarkup, "}", lx.line)
	lx.pos++
}

// markupText cleans up the whitespace in text children the same way as JSX
// (Babel's cleanJSXElementLiteralChild) and decodes HTML entities.
func markupText(raw string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")

	lastNonEmpty := 0
	for i, line := range lines {
		if strings.Trim(line, " \t") != "" {
			lastNonEmpty = i
		}
	}

	var b strings.Builder
	for i, line := range lines {
		line = strings.ReplaceAll(line, "\t", " ")
		if i > 0 {
			line = strings.TrimLeft(line, " ")
		}
		if i < len(lines)-1 {
			line = strings.TrimRight(line, " ")
		}
		if line != "" {
			b.WriteString(line)
			if i != lastNonEmpty {
				b.WriteByte(' ')
			}
		}
	}

	return markupDecodeEntities(b.String())
}

var markupEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'", "nbsp": " ",
	"copy": "©", "reg": "®", "trade": "™", "hellip": "…", "mdash": "—",
	"ndash": "–", "lsquo": "‘", "rsquo": "’", "ldquo": "“", "rdquo": "”",
	"bull": "•", "middot": "·", "times": "×", "larr": "←", "rarr": "→",
	"uarr": "↑", "darr": "↓", "lbrace": "{", "rbrace": "}",
}

// markupDecodeEntities decodes named (see markupEntities) and numeric HTML entities.
// Anything else is left as is.
func markupDecodeEntities(s string) string {
	if !strings.Contains(s, "&") {
		return s
	}

	var b strings.Builder
	for {
		amp := strings.IndexByte(s, '&')
		if amp < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:amp])
		s = s[amp:]

		semi := strings.IndexByte(s, ';')
		if semi > 1 && semi < 12 {
			if decoded, ok := markupDecodeEntity(s[1:semi]); ok {
				b.WriteString(decoded)
				s = s[semi+1:]
				continue
			}
		}
		b.WriteByte('&')
		s = s[1:]
	}
}

func markupDecodeEntity(name string) (string, bool) {
	if !strings.HasPrefix(name, "#") {
		decoded, ok := markupEntities[name]
		return decoded, ok
	}

	var code uint64
	var err error
	if strings.HasPrefix(name, "#x") || strings.HasPrefix(name, "#X") {
		code, err = strconv.ParseUint(name[2:], 16, 32)
	} else {
		code, err = strconv.ParseUint(name[1:], 10, 32)
	}
	if err != nil || !utf8.ValidRune(rune(code)) {
		return "", false
	}
	return string(rune(code)), true
}

/* }}} */

/* Parser {{{ */

func (p *midnightParser) isMarkup(value string) bool {
	return p.cur.Type == midnightMarkup && p.cur.Value == value
}

func (p *midnightParser) expectMarkup(value string) {
	if p.cur.Type == midnightEOF {
		p.throwEOF("unexpected eof (expected %s)", value)
	}
	if !p.isMarkup(value) {
		p.throw(p.cur.Line, "expected '%s' got '%s'", value, p.cur.Value)
	}
	p.consume()
}

// markupFactory returns the function elements are lowered to: MarkupFactory if set,
// otherwise `h` if it's declared, otherwise `require('midnight.markup').h` (the
// "automatic runtime", imported once at the top of the chunk, see
// markupRuntimeImport).
func (p *midnightParser) markupFactory(line int) ast.Expr {
	name := p.opts.MarkupFactory
	if name == "" {
		if p.lookup("h") != "" {
			return p.resolve("h", line)
		}
		if p.markupRuntime == "" {
			p.markupRuntime = p.newTmpName()
		}
		return midnightIdent(p.markupRuntime, line)
	}
	parts := strings.Split(name, ".")
	expr := p.resolve(parts[0], line)
	for _, key := range parts[1:] {
		expr = midnightAttr(expr, midnightString(key, line), line)
	}
	return expr
}

// MarkupRuntimeModule is the module the automatic markup factory is imported from.
const MarkupRuntimeModule = "midnight.markup"

// markupRuntimeImport is `local <markupRuntime> = require('midnight.markup').h`.
func (p *midnightParser) markupRuntimeImport() ast.Stmt {
	runtime := midnightCall(midnightIdent("require", 1), []ast.Expr{midnightString(MarkupRuntimeModule, 1)}, 1)
	return midnightLocal([]string{p.markupRuntime}, []ast.Expr{midnightAttr(runtime, midnightString("h", 1), 1)}, 1)
}

// markupTag returns the expression for a tag name: a string for HTML elements
// (lowercase, or containing `-` / `:`), otherwise a (dotted) name.
func (p *midnightParser) markupTag(name string, line int) ast.Expr {
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		expr := p.resolve(parts[0], line)
		for _, key := range parts[1:] {
			expr = midnightAttr(expr, midnightString(key, line), line)
		}
		return expr
	}
	if c := name[0]; 'a' <= c && c <= 'z' || strings.ContainsAny(name, "-:") {
		return midnightString(name, line)
	}
	return p.resolve(name, line)
}

func (p *midnightParser) markupElement() ast.Expr {
	line := p.cur.Line
	p.expectMarkup("<")

	if p.isMarkup(">") { // fragment
		p.consume()
		children := p.markupChildren("", line)
		args := append([]ast.Expr{midnightLine(&ast.NilExpr{}, line), midnightLine(&ast.NilExpr{}, line)}, children...)
		return markupCall(p.markupFactory(line), args, line)
	}

	name := p.consume()
	args := []ast.Expr{p.markupTag(name, line), p.markupAttributes(line)}

	if p.isMarkup("/>") {
		p.consume()
	} else {
		p.expectMarkup(">")
		args = append(args, p.markupChildren(name, line)...)
	}

	return markupCall(p.markupFactory(line), args, line)
}

// markupCall is the call an element is lowered to, marked as an element.
func markupCall(factory ast.Expr, args []ast.Expr, line int) ast.Expr {
	call := midnightCall(factory, args, line)
	call.Element = true
	return call
}

// markupAttributes parses an opening tag's attributes into the props
// expression: nil when there are none, a table constructor, or (when there
// are spreads) a function call that merges them in order.
func (p *midnightParser) markupAttributes(line int) ast.Expr {
	type attribute struct {
		field  *ast.Field
		spread ast.Expr
	}
	attrs := []attribute{}
	hasSpread := false

	for !p.isMarkup(">") && !p.isMarkup("/>") {
		attrLine := p.cur.Line

		if p.isMarkup("{") {
			p.consume()
			p.expect("...")
			spread := p.expression(1)
			p.expectMarkup("}")
			attrs = append(attrs, attribute{spread: spread})
			hasSpread = true
			continue
		}

		if p.cur.Type != midnightMarkupName {
			p.throw(attrLine, "unexpected token '%s' in markup element", p.cur.Value)
		}
		key := midnightString(p.consume(), attrLine)

		var value ast.Expr = midnightLine(&ast.TrueExpr{}, attrLine)
		if p.isMarkup("=") {
			p.consume()
			switch {
			case p.cur.Type == midnightSingleQuote || p.cur.Type == midnightDoubleQuote:
				value = p.stringExpr()
			case p.isMarkup("{"):
				p.consume()
				if p.isMarkup("}") {
					p.throw(attrLine, "markup attribute value can't be empty")
				}
				value = p.markupValue(p.expression(1))
				p.expectMarkup("}")
			}
		}

		attrs = append(attrs, attribute{field: &ast.Field{Key: key, Value: value}})
	}

	if len(attrs) == 0 {
		return midnightLine(&ast.NilExpr{}, line)
	}

	if !hasSpread {
		fields := make([]*ast.Field, len(attrs))
		for i, attr := range attrs {
			fields[i] = attr.field
		}
		return midnightLine(&ast.TableExpr{Fields: fields}, line)
	}

	// (function() local t = {} t.a = 1 for k, v in pairs(spread or {}) { t[k] = v } return t end)()
	//
	// Spreading nil is a no-op (same as JSX).
	t, k, v := p.newTmpName(), p.newTmpName(), p.newTmpName()
	stmts := []ast.Stmt{midnightLocal([]string{t}, []ast.Expr{midnightLine(&ast.TableExpr{}, line)}, line)}
	for _, attr := range attrs {
		if attr.spread == nil {
			stmts = append(stmts, midnightAssign(midnightAttr(midnightIdent(t, line), attr.field.Key, line), attr.field.Value, line))
			continue
		}
		spread := midnightLine(&ast.LogicalOpExpr{Operator: "or", Lhs: attr.spread, Rhs: midnightLine(&ast.TableExpr{}, line)}, line)
		stmts = append(stmts, midnightLine(&ast.GenericForStmt{
			Names: []string{k, v},
			Exprs: []ast.Expr{midnightCall(midnightIdent("pairs", line), []ast.Expr{spread}, line)},
			Stmts: []ast.Stmt{midnightAssign(midnightAttr(midnightIdent(t, line), midnightIdent(k, line), line), midnightIdent(v, line), line)},
		}, line))
	}
	stmts = append(stmts, midnightLine(&ast.ReturnStmt{Exprs: []ast.Expr{midnightIdent(t, line)}}, line))

	fn := midnightLine(&ast.FunctionExpr{ParList: &ast.ParList{}, Stmts: stmts}, line)
	return midnightCall(fn, []ast.Expr{}, line)
}

// markupChildren parses an element's children, up to and including the closing
// tag (which must match `name`).
func (p *midnightParser) markupChildren(name string, line int) []ast.Expr {
	children := []ast.Expr{}

	for {
		switch {
		case p.cur.Type == midnightMarkupText:
			children = append(children, midnightString(p.cur.Value, p.cur.Line))
			p.consume()
		case p.isMarkup("<"):
			children = append(children, p.markupElement())
		case p.isMarkup("{"):
			p.consume()
			if !p.isMarkup("}") { // `{}` / `{--[[ comment ]]}` are ignored
				children = append(children, p.markupValue(p.expression(1)))
			}
			p.expectMarkup("}")
		case p.isMarkup("</"):
			closeLine := p.cur.Line
			p.consume()
			closing := ""
			if p.cur.Type == midnightMarkupName {
				closing = p.consume()
			}
			if closing != name {
				p.throw(closeLine, "expected closing tag '</%s>' (for line %d) got '</%s>'", name, line, closing)
			}
			p.expectMarkup(">")
			return children
		case p.cur.Type == midnightEOF:
			p.throwEOF("unterminated markup element '<%s>'", name)
		default:
			p.throw(p.cur.Line, "unexpected token '%s' in markup element", p.cur.Value)
		}
	}
}

// markupValue truncates multiple values to one (same as parentheses).
func (p *midnightParser) markupValue(expr ast.Expr) ast.Expr {
	switch ex := expr.(type) {
	case *ast.FuncCallExpr:
		ex.AdjustRet = true
	case *ast.Comma3Expr:
		ex.AdjustRet = true
	}
	return expr
}

/* }}} */
