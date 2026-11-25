package mngen

import (
	"bytes"
	"fmt"
	"strings"
)

/* Literals {{{ */

// number returns a number literal valid in Midnight (Lua allows `5.` and
// `5.e3`, Midnight needs a digit after the `.`).
func number(value string) string {
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "0x") {
		return value
	}
	if i := strings.IndexByte(value, '.'); i >= 0 && (i == len(value)-1 || !isDigit(value[i+1])) {
		return value[:i+1] + "0" + value[i+1:]
	}
	return value
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// string writes a string literal. Strings that span lines in the source are
// written as block strings (`[[...]]`) when possible, to keep their lines.
func (g *generator) string(s string) {
	if leadingNewline, ok := g.sc.multiline[g.line]; ok {
		if lit, ok := blockString(s, leadingNewline); ok && !g.commentsWithin(strings.Count(lit, "\n")) {
			g.write(lit)
			return
		}
	}
	g.write(quote(s))
}

// commentsWithin reports whether the source has comments on the next n lines
// (including this one), which writing n newlines would skip past.
func (g *generator) commentsWithin(n int) bool {
	return g.nextComment < len(g.sc.comments) && g.sc.comments[g.nextComment].line < g.line+n
}

// blockString returns s as a block string, if it's multiline and can be one.
// In Midnight, `{` in a block string starts an interpolation, and `\{` / `\}`
// are escapes. leadingNewline starts it w/ a (skipped) newline.
func blockString(s string, leadingNewline bool) (string, bool) {
	if !strings.Contains(s, "\n") {
		return "", false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 32 && c != '\n' && c != '\t' || c == 127 {
			return "", false
		}
	}

	// Escape `{` as `\{`, and a `}` after a `\` (which would be read as `\}`).
	// A `\` that isn't before a brace is kept as is, so `\` + `\}` is `\}`.
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == '{':
			b.WriteString(`\{`)
		case c == '}' && i > 0 && s[i-1] == '\\':
			b.WriteString(`\}`)
		default:
			b.WriteByte(c)
		}
	}
	s = b.String()

	eq := ""
	for {
		closeQuote := "]" + eq + "]"
		if strings.Index(s+closeQuote, closeQuote) == len(s) {
			break
		}
		eq += "="
	}

	// (a newline right after the opening quote is skipped)
	if leadingNewline || strings.HasPrefix(s, "\n") {
		s = "\n" + s
	}
	return "[" + eq + "[" + s + "]" + eq + "]", true
}

// quote returns a quoted string literal. Single quoted strings have no
// interpolation, so they're preferred.
func quote(s string) string {
	q := byte('\'')
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = '"'
	}
	return quoteWith(s, q)
}

// quoteWith returns s quoted w/ q (' or ").
func quoteWith(s string, q byte) string {
	var b bytes.Buffer
	b.WriteByte(q)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case c == q || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '{' && q == '"':
			b.WriteString(`\{`)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		case c < 32 || c == 127:
			fmt.Fprintf(&b, "\\%03d", c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte(q)
	return b.String()
}

/* }}} */
