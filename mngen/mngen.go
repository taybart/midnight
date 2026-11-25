// Package mngen converts Lua source to Midnight (the reverse of luagen).
//
// The conversion is a syntax translation: the output is the same program, w/
// Midnight syntax (`{ }` blocks, `->` functions, `&&` / `||` / `!` / `!=`),
// not rewritten to use Midnight features (except opt-in ones, see Options).
// Comments and line breaks are kept.
//
// Code that has no exact Midnight equivalent isn't converted. Instead, Convert
// returns an error for each problem, ex. a name that is a Midnight keyword
// (`continue`, `global`, `module`, as in Lua 5.1's `module(...)`).
//
// Every result is checked: the output must parse to the same program as the
// input, or Convert fails. The only difference allowed is the grouping of
// `..` chains (`a .. b .. c` is `a .. (b .. c)` in Lua and `(a .. b) .. c` in
// Midnight), which gives the same result unless a `__concat` metamethod
// depends on it.
package mngen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/parse"
)

// Error is a problem converting a line of Lua.
type Error struct {
	Source  string
	Line    int
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s:%d: %s", e.Source, e.Line, e.Message)
}

// Errors are all the problems found converting a file.
type Errors []*Error

func (es Errors) Error() string {
	msgs := make([]string, len(es))
	for i, e := range es {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Convert converts Lua source to Midnight. name is the source's name in
// errors. The error is an Errors if the Lua uses something that can't be
// converted, or another error if it doesn't parse.
func Convert(source []byte, name string) ([]byte, error) {
	return ConvertWith(source, name, Options{})
}

// ConvertWith is Convert w/ opt-in conversions (see Options).
func ConvertWith(source []byte, name string, opts Options) ([]byte, error) {
	text := strings.ReplaceAll(string(source), "\r\n", "\n")

	// Lua skips a first line starting w/ `#`, Midnight only `#!` (which scan
	// keeps as a comment).
	code := text
	if strings.HasPrefix(text, "#") {
		if !strings.HasPrefix(text, "#!") {
			return nil, Errors{{Source: name, Line: 1, Message: "a first line starting w/ '#' must be a '#!' line in Midnight"}}
		}
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			end = len(text)
		}
		code = text[end:]
	}

	chunk, err := parse.Parse(strings.NewReader(code), name)
	if err != nil {
		return nil, err
	}

	g := &generator{source: name, opts: opts, line: 1, fresh: true, sc: scan(text), usedParens: map[int]int{}, usedBraces: map[int]int{}}
	g.stmts(chunk)
	g.finish()

	if len(g.errs) > 0 {
		sort.SliceStable(g.errs, func(i, j int) bool { return g.errs[i].Line < g.errs[j].Line })
		return nil, g.errs
	}

	formatted, err := format.Format(g.buf, format.Options{})
	if err != nil {
		return nil, Errors{{Source: name, Line: errorLine(err), Message: "conversion bug, output doesn't parse: " + errorMessage(err)}}
	}

	if line, ok := verify(chunk, formatted, opts); !ok {
		return nil, Errors{{Source: name, Line: line, Message: "conversion bug, output parses to a different program"}}
	}

	return formatted, nil
}

type generator struct {
	source string
	opts   Options
	buf    []byte

	// Current output line (output lines follow the source's).
	line int
	// Block depth, for indentation
	depth int
	// Whether we are at the start of a line (nothing written yet)
	fresh bool
	// Line the last statement ended on (to separate statements on one line).
	stmtEnd int

	sc          *scanned
	nextComment int
	// Openers (see scanned) used so far, by line.
	usedParens, usedBraces map[int]int

	errs Errors
}

func (g *generator) errorf(line int, format string, args ...any) {
	g.errs = append(g.errs, &Error{Source: g.source, Line: line, Message: fmt.Sprintf(format, args...)})
}

/* Output {{{ */

func (g *generator) write(s string) {
	if g.fresh {
		g.buf = append(g.buf, strings.Repeat("  ", g.depth)...)
	}
	g.buf = append(g.buf, s...)
	g.line += strings.Count(s, "\n")
	g.fresh = false
}

// space separates tokens, unless we are at the start of a line.
func (g *generator) space() {
	if !g.fresh {
		g.write(" ")
	}
}

// newline ends the current line, after the comments on it.
func (g *generator) newline() {
	line := g.line
	g.flushComments()
	if g.line > line {
		return // (a multiline comment moved to the next line)
	}
	g.buf = append(g.buf, '\n')
	g.line++
	g.fresh = true
}

// flushComments writes the source's comments up to the current line.
func (g *generator) flushComments() {
	for g.nextComment < len(g.sc.comments) && g.sc.comments[g.nextComment].line <= g.line {
		g.space()
		g.write(g.sc.comments[g.nextComment].text)
		g.nextComment++
	}
}

// sync moves the output to `line` (never backwards), to keep the source's
// line breaks (and comments).
func (g *generator) sync(line int) {
	for g.line < line {
		g.newline()
	}
}

func (g *generator) finish() {
	if n := len(g.sc.comments); n > g.nextComment {
		g.sync(g.sc.comments[n-1].line)
	}
	g.flushComments()
	g.write("\n")
}

// keyword writes a keyword, separated from the previous token.
func (g *generator) keyword(keyword string) {
	g.space()
	g.write(keyword)
}

// opener returns the line of the closing token for the next `(` / `{` in the
// source on the current line (or 0 if unknown).
func (g *generator) opener(openers map[int][]*opener, used map[int]int) int {
	i := used[g.line]
	if i >= len(openers[g.line]) {
		return 0
	}
	used[g.line]++
	return openers[g.line][i].closeLine
}

// open writes a `(` that is in the source, and returns a function writing
// its `)`, on the same line as in the source.
func (g *generator) open() func() {
	closeLine := g.opener(g.sc.parens, g.usedParens)
	g.write("(")
	return func() {
		g.sync(closeLine)
		g.write(")")
	}
}

/* }}} */

/* Names {{{ */

// Words that are names in Lua but keywords in Midnight.
var midnightKeywords = map[string]bool{"continue": true, "global": true, "module": true}

// checkName checks a name (variable, parameter or label) is valid in Midnight.
func (g *generator) checkName(name string, line int, what string) string {
	if midnightKeywords[name] {
		hint := "rename it"
		switch {
		case what == "label" && name == "continue":
			hint = "use Midnight's `continue` instead of `goto continue`"
		case name == "module" && what == "global":
			hint = "Lua 5.1's module() isn't supported, return a table instead"
		}
		g.errorf(line, "%s '%s' is a Midnight keyword (%s)", what, name, hint)
	}
	return name
}

// isName reports whether s can be written as a name in Midnight (ex. a table
// key `t.s` or `{ s = 1 }`).
func isName(s string) bool {
	if s == "" || midnightKeywords[s] || luaKeywords[s] {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9') {
			return false
		}
	}
	return true
}

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true, "or": true,
	"repeat": true, "return": true, "then": true, "true": true, "until": true,
	"while": true,
}

/* }}} */
