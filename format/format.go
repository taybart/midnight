// Package format formats Midnight source.
//
// The formatter works on tokens (see parse.MidnightRawTokens), not a syntax tree,
// so it keeps the source's line breaks and only normalizes:
//
//   - indentation (2 spaces per open bracket, plus one for continuation lines)
//   - spacing between tokens
//   - blank lines (at most one, none at the start / end of a block)
//   - trailing commas (added to multiline tables, removed from single line ones)
//   - quotes (`"abc"` becomes `'abc'` when that needs no escapes)
//   - comments (`--x` becomes `-- x`)
//
// Strings (including interpolations) and comments are kept as written. Every
// result is checked to parse to the same program as the input.
package format

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/taybart/midnight/parse"
)

type Options struct {
	// Experimental Midnight extensions (needed to parse sources that use them).
	Midnight parse.MidnightOptions
}

const indentUnit = "  "

type token struct {
	parse.MidnightRawToken
	// Whether there was whitespace before this token in the source.
	spaced bool
	// Whether this token was added by the formatter (ex. a trailing comma).
	synthetic bool
	// Whether to start a new line at this token (ex. after `{` in `{ a,\n}`).
	breakBefore bool
	// Column of the token in the source, and the indentation (width) of its
	// line (only set for comments).
	column, indent int
}

// Format formats Midnight source. It returns an error if the source doesn't parse,
// or if formatting would change the program (a formatter bug).
func Format(source []byte, opts Options) ([]byte, error) {
	text := strings.ReplaceAll(string(source), "\r\n", "\n")

	before, err := dump(text, opts)
	if err != nil {
		return nil, err
	}

	raw, err := parse.MidnightRawTokensWithOptions(text, "<format>", opts.Midnight)
	if err != nil {
		return nil, err
	}

	tokens := make([]*token, len(raw))
	for i, t := range raw {
		tokens[i] = &token{MidnightRawToken: t, spaced: i == 0 || t.Start > raw[i-1].End}
		if t.Kind == parse.RawComment {
			lineStart := strings.LastIndexByte(text[:t.Start], '\n') + 1
			prefix := text[lineStart:t.Start]
			tokens[i].column = utf8.RuneCountInString(prefix)
			tokens[i].indent = len(prefix) - len(strings.TrimLeft(prefix, " \t"))
		}
	}

	tokens = trailingCommas(tokens)
	expandBraces(tokens)
	for _, t := range tokens {
		if t.Kind == parse.RawString {
			t.Text = preferSingleQuotes(t.Text)
		} else if t.Kind == parse.RawComment {
			t.Text = normalizeComment(t.Text)
		}
	}

	formatted := render(tokens)

	after, err := dump(formatted, opts)
	if err != nil {
		return nil, fmt.Errorf("formatter bug, output doesn't parse: %w", err)
	}
	if after != before {
		return nil, fmt.Errorf("formatter bug, output parses to a different program")
	}

	return []byte(formatted), nil
}

func dump(text string, opts Options) (string, error) {
	chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(text), "<format>", opts.Midnight)
	if err != nil {
		return "", err
	}
	return parse.Dump(chunk), nil
}

/* Token classes {{{ */

var keywords = map[string]bool{
	"break": true, "continue": true, "do": true, "else": true, "elseif": true,
	"for": true, "function": true, "global": true, "goto": true, "if": true,
	"in": true, "local": true, "module": true, "repeat": true, "return": true,
	"until": true, "while": true,
}

// Keywords that may be followed by an expression (so a following `-` is
// unary, and `(` / `[` start an expression).
var expressionKeywords = map[string]bool{
	"elseif": true, "if": true, "in": true, "return": true, "until": true, "while": true,
}

var binaryOperators = map[string]bool{
	"=": true, "||": true, "&&": true, "==": true, "!=": true, "<=": true,
	">=": true, "<": true, ">": true, "|": true, "~": true, "&": true, "<<": true,
	">>": true, "..": true, "+": true, "-": true, "*": true, "/": true, "//": true,
	"%": true, "^": true, "->": true, "=>": true,
}

// Operators that can be followed by `=` for compound assignment (ex. `+=`).
var assignmentOperators = map[string]bool{
	"||": true, "&&": true, "..": true, "+": true, "-": true, "*": true,
	"/": true, "//": true, "%": true, "^": true, "|": true, "~": true, "&": true,
	"<<": true, ">>": true,
}

func is(t *token, values ...string) bool {
	if t == nil || t.Kind != parse.RawSymbol && t.Kind != parse.RawWord {
		return false
	}
	for _, v := range values {
		if t.Text == v {
			return true
		}
	}
	return false
}

func isOpen(t *token) bool  { return is(t, "(", "[", "{") }
func isClose(t *token) bool { return is(t, ")", "]", "}") }

func isKeyword(t *token) bool { return t != nil && t.Kind == parse.RawWord && keywords[t.Text] }

// isValue checks for tokens that end an expression (ex. names, literals, and
// closing brackets).
func isValue(t *token) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case parse.RawNumber, parse.RawString, parse.RawMarkup:
		return true
	case parse.RawWord:
		return !keywords[t.Text]
	}
	return is(t, ")", "]", "}", "...")
}

func isBinaryOperator(t *token) bool {
	return t != nil && t.Kind == parse.RawSymbol && binaryOperators[t.Text]
}

/* }}} */

/* Rewrites {{{ */

// trailingCommas adds trailing commas to multiline tables, and removes them
// from single line tables.
func trailingCommas(tokens []*token) []*token {
	type open struct {
		index   int
		isTable bool
	}

	var stack []open
	removed := map[int]bool{}
	insertAfter := map[int]bool{}

	for i, t := range tokens {
		switch {
		case is(t, "(", "["):
			stack = append(stack, open{i, false})
		case is(t, "{"):
			stack = append(stack, open{i, isTableBrace(tokens, i)})
		case isClose(t) && len(stack) > 0:
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !is(t, "}") || !o.isTable {
				continue
			}

			// Last non comment token in the table.
			last := i - 1
			for last > o.index && tokens[last].Kind == parse.RawComment {
				last--
			}
			if last == o.index {
				continue // empty
			}

			if tokens[o.index].Line == t.Line {
				if is(tokens[last], ",") {
					removed[last] = true
				}
			} else if !is(tokens[last], ",", ";") {
				insertAfter[last] = true
			}
		}
	}

	result := make([]*token, 0, len(tokens)+len(insertAfter))
	for i, t := range tokens {
		if removed[i] {
			continue
		}
		result = append(result, t)
		if insertAfter[i] {
			comma := &token{synthetic: true}
			comma.Kind = parse.RawSymbol
			comma.Text = ","
			comma.Line, comma.EndLine = t.EndLine, t.EndLine
			result = append(result, comma)
		}
	}
	return result
}

// expandBraces puts the contents of multiline `{ ... }` (blocks and tables) on
// their own lines (ex. `if x { a\n}` becomes `if x {\n  a\n}`).
func expandBraces(tokens []*token) {
	var stack []int
	for i, t := range tokens {
		switch {
		case is(t, "{"):
			stack = append(stack, i)
		case is(t, "}") && len(stack) > 0:
			o := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if o == i-1 || tokens[o].Line == t.Line {
				continue // empty, or single line
			}
			if next := tokens[o+1]; next.Line == tokens[o].EndLine && next.Kind != parse.RawComment {
				next.breakBefore = true
			}
			if tokens[i-1].EndLine == t.Line {
				t.breakBefore = true
			}
		case is(t, "(", "["):
			stack = append(stack, -1)
		case is(t, ")", "]") && len(stack) > 0:
			stack = stack[:len(stack)-1]
		}
	}
}

// isTableBrace checks whether the `{` at tokens[i] starts a table (or a
// destructure) rather than a block.
func isTableBrace(tokens []*token, i int) bool {
	if i == 0 {
		return false
	}
	prev := tokens[i-1]
	switch {
	case is(prev, "->", "=>"):
		return false // arrow function bodies are always blocks
	case isValue(prev):
		return false // ex. `if x {`, `in ... {`, `f() {`
	case prev.Kind == parse.RawSymbol:
		return true
	case prev.Kind == parse.RawWord:
		return expressionKeywords[prev.Text] || is(prev, "local", "global", "module", "for")
	}
	return false
}

// preferSingleQuotes rewrites `"abc"` as `'abc'`, when the content needs no
// escapes and has no interpolation.
func preferSingleQuotes(s string) string {
	if len(s) < 2 || s[0] != '"' || strings.ContainsAny(s[1:len(s)-1], `'\{}`) {
		return s
	}
	return "'" + s[1:len(s)-1] + "'"
}

// normalizeComment adds a space after `--` in line comments (ex. `--x`), and
// removes trailing whitespace.
func normalizeComment(s string) string {
	if !strings.HasPrefix(s, "--") || strings.HasPrefix(s, "--[") {
		return s
	}
	s = strings.TrimRight(s, " \t")
	if len(s) > 2 && isWordByte(s[2]) {
		return "-- " + s[2:]
	}
	return s
}

func isWordByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}

/* }}} */

/* Printing {{{ */

// lineGroup is the tokens starting on one source line (a multiline token's
// last line continues its group).
type lineGroup struct {
	tokens []*token
	// Blank lines in the source before this group.
	blankBefore bool
}

func groupLines(tokens []*token) []lineGroup {
	var groups []lineGroup
	for i, t := range tokens {
		if i == 0 || t.Line > tokens[i-1].EndLine || t.breakBefore {
			blank := i > 0 && t.Line > tokens[i-1].EndLine+1
			groups = append(groups, lineGroup{blankBefore: blank})
		}
		g := &groups[len(groups)-1]
		g.tokens = append(g.tokens, t)
	}
	return groups
}

// outputLine is a formatted line, w/ its trailing comment kept separate so
// comments can be aligned.
type outputLine struct {
	code    string
	comment string // trailing comment, if any
	// The trailing comment's column in the source, adjusted for the change in
	// the line's indentation.
	column int
	blank  bool // preceded by a blank line
}

func render(tokens []*token) string {
	var lines []outputLine

	// Open brackets. All brackets opened on a line share one indent level (ex.
	// `f({` only indents once).
	type bracket struct {
		text  string
		base  int // indent of the line that opened the bracket
		inner int // indent of lines inside the bracket
	}
	var open []bracket
	var prev *token // previous non comment token (across lines)
	var last *token // previous line's last token

	groups := groupLines(tokens)
	for gi, g := range groups {
		first := g.tokens[0]

		depth := 0
		leading := 0
		if isClose(first) {
			// Lines starting w/ a closing bracket line up w/ the line that opened
			// it (ex. `}, x)` after `f(() -> {`).
			if len(open) > 0 {
				depth = open[len(open)-1].base
			}
			for leading < len(g.tokens) && isClose(g.tokens[leading]) {
				if len(open) > 0 {
					open = open[:len(open)-1]
				}
				leading++
			}
		} else {
			inParens := false
			if len(open) > 0 {
				depth = open[len(open)-1].inner
				inParens = open[len(open)-1].text != "{"
			}
			// Parentheses / brackets already indent their contents.
			if gi > 0 && !inParens && isContinuation(prev, first) {
				depth++
			}
		}

		line := outputLine{blank: gi > 0 && g.blankBefore && !isOpen(last) && !isClose(first)}

		if first.Kind == parse.RawComment && strings.HasPrefix(first.Text, "#!") {
			line.code = first.Text
		} else {
			code := g.tokens
			if n := len(code); n > 1 && code[n-1].Kind == parse.RawComment && !strings.Contains(code[n-1].Text, "\n") {
				c := code[n-1]
				line.comment = c.Text
				line.column = c.column - c.indent + depth*len(indentUnit)
				code = code[:n-1]
			}

			var b strings.Builder
			b.WriteString(strings.Repeat(indentUnit, depth))
			for i, t := range code {
				if i > 0 && spaceBetween(prevOf(code, i, prev), t, prevOf(code, i-1, prev)) {
					b.WriteByte(' ')
				}
				b.WriteString(t.Text)
			}
			line.code = b.String()
		}
		lines = append(lines, line)

		for _, t := range g.tokens[leading:] {
			switch {
			case isOpen(t):
				open = append(open, bracket{text: t.Text, base: depth, inner: depth + 1})
			case isClose(t) && len(open) > 0:
				open = open[:len(open)-1]
			}
		}

		last = g.tokens[len(g.tokens)-1]
		// Skip comments when looking back (ex. for continuation lines).
		for _, t := range g.tokens {
			if t.Kind != parse.RawComment {
				prev = t
			}
		}
	}

	alignComments(lines)

	var out bytes.Buffer
	for _, line := range lines {
		if line.blank {
			out.WriteByte('\n')
		}
		out.WriteString(line.code)
		if line.comment != "" {
			out.WriteString(line.comment)
		}
		out.WriteByte('\n')
	}
	return out.String()
}

// alignComments places trailing comments. Comments on consecutive lines that
// were aligned in the source (started at the same column) stay aligned, at
// their original column if the code still fits before it. Other comments get
// a single space.
func alignComments(lines []outputLine) {
	width := func(l outputLine) int {
		return utf8.RuneCountInString(l.code[strings.LastIndexByte(l.code, '\n')+1:])
	}

	for i := 0; i < len(lines); {
		if lines[i].comment == "" {
			i++
			continue
		}

		// Run of consecutive lines w/ trailing comments at the same column.
		end := i + 1
		for end < len(lines) && lines[end].comment != "" && !lines[end].blank &&
			lines[end].column == lines[i].column && !strings.Contains(lines[end].code, "\n") {
			end++
		}

		column := 0
		if end-i > 1 {
			for _, l := range lines[i:end] {
				column = max(column, width(l)+1)
			}
			column = max(column, lines[i].column)
		}

		for j := i; j < end; j++ {
			pad := max(column-width(lines[j]), 1)
			lines[j].comment = strings.Repeat(" ", pad) + lines[j].comment
		}
		i = end
	}
}

// prevOf returns the token before tokens[i] (in the line, or the previous
// line's last token).
func prevOf(tokens []*token, i int, prevLine *token) *token {
	if i > 0 {
		return tokens[i-1]
	}
	return prevLine
}

// isContinuation checks whether a line continues the previous line's
// expression (and so is indented once more).
func isContinuation(prev, first *token) bool {
	if prev == nil || isClose(first) || is(first, "{") {
		return false
	}
	// Previous line ends w/ a binary operator (ex. `local x =`, `a ..`).
	if isBinaryOperator(prev) && !is(prev, "->", "=>") {
		return true
	}
	// Line starts w/ a binary operator or index (ex. method chains). `-` and
	// `~` are left out, since they may be unary.
	return is(first, ".", ":") || isBinaryOperator(first) && !is(first, "-", "~", "=", "->", "=>")
}

// isUnary checks whether `-` / `~` (at `t`, after `prev`) is a unary operator.
func isUnary(t, prev *token) bool {
	if is(t, "!", "#") {
		return true
	}
	if !is(t, "-", "~") {
		return false
	}
	return prev == nil || !isValue(prev)
}

// spaceBetween checks whether to put a space between tokens a and b on a line
// (`beforeA` is the token before `a`, for unary operators).
func spaceBetween(a, b, beforeA *token) bool {
	switch {
	case b.Kind == parse.RawComment:
		return true
	case a.Kind == parse.RawComment:
		return b.spaced // block comment followed by code
	case is(b, ",", ";"):
		return false
	case is(a, ",", ";"):
		return true
	case is(a, "(", "["), is(b, ")", "]"):
		return false
	case is(a, "{"):
		return !is(b, "}")
	case is(b, "}"):
		return !is(a, "{")
	case is(a, "."), is(b, "."):
		return false
	case is(a, ":", "::"), is(b, ":", "::"):
		return b.spaced // method calls vs `{ a: b }` destructures, labels
	case assignmentOperators[a.Text] && a.Kind == parse.RawSymbol && is(b, "=") && !b.spaced && !b.synthetic:
		return false // compound assignment (ex. `+=`)
	case isUnary(a, beforeA):
		return false
	case isBinaryOperator(a), isBinaryOperator(b):
		return true
	case is(a, "!", "#"):
		return false
	case is(a, "..."):
		return b.spaced || !(b.Kind == parse.RawWord) // `...rest` parameters
	case is(b, "("):
		if is(a, "match") {
			return b.spaced // `match(x)` call vs `match (x) {` statement
		}
		return !isValue(a) && !is(a, "function")
	case is(b, "["):
		return !isValue(a)
	}
	return true
}

/* }}} */
