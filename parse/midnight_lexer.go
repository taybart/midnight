package parse

import (
	"bytes"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/taybart/midnight/ast"
)

// Midnight tokenizer. Mirrors Erde's erde/tokenize.erde:
// strings are split into delimiter / content / interpolation tokens, so the
// parser sees interpolated expressions as ordinary tokens.

type midnightTokenType int

const (
	midnightEOF midnightTokenType = iota
	midnightSymbol
	midnightWord
	midnightNumber
	midnightSingleQuote // `'` delimiter
	midnightDoubleQuote // `"` delimiter
	midnightBlockString // `[==[` / `]==]` delimiter
	midnightStringContent
	midnightInterpolation // `{` / `}` around an interpolated expression
	midnightMarkup        // markup syntax: `<`, `</`, `>`, `/>`, `=`, `{`, `}`
	midnightMarkupName    // markup tag / attribute name
	midnightMarkupText    // markup text (cleaned up, see markupText)
)

type midnightToken struct {
	Type  midnightTokenType
	Value string // decoded value for midnightStringContent
	Line  int
}

var midnightSymbols = map[string]bool{
	"->": true, "=>": true, "...": true, "::": true,
	"||": true, "&&": true, "==": true, "!=": true, "<=": true, ">=": true,
	"..": true, "//": true, "<<": true, ">>": true,
}

type midnightLexer struct {
	source string
	text   string
	pos    int
	line   int
	tokens []midnightToken

	// Whether to lex markup elements (see MidnightOptions.Markup).
	markup bool

	// Raw tokens (for tooling, see MidnightRawTokens), or nil when not needed.
	raw *[]MidnightRawToken
	// > 0 while lexing a raw token (ex. inside a string's interpolation, which
	// is part of the string's raw token).
	rawDepth int
}

// MidnightRawKind is the kind of an MidnightRawToken.
type MidnightRawKind int

const (
	RawWord MidnightRawKind = iota
	RawNumber
	RawSymbol
	RawString  // whole string literal, including interpolations
	RawComment // also a leading `#!` line
	RawMarkup  // whole markup element (see MidnightOptions.Markup)
)

// MidnightRawToken is a token as written in the source (ex. for formatters).
type MidnightRawToken struct {
	Kind MidnightRawKind
	Text string
	// Byte offsets of the token in the source.
	Start, End int
	// Lines the token starts and ends on (they differ for multiline strings
	// and comments).
	Line, EndLine int
}

func (lx *midnightLexer) throw(line int, format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: lx.source, Line: line},
		Message: fmt.Sprintf(format, args...),
	})
}

// throwEOF throws an error for input that ends early (see midnightParser.throwEOF).
func (lx *midnightLexer) throwEOF(format string, args ...interface{}) {
	panic(&Error{
		Pos:     ast.Position{Source: lx.source, Line: EOF},
		Message: fmt.Sprintf(format, args...),
	})
}

func (lx *midnightLexer) peek(n int) byte {
	if lx.pos+n < len(lx.text) {
		return lx.text[lx.pos+n]
	}
	return 0
}

func (lx *midnightLexer) eof() bool { return lx.pos >= len(lx.text) }

func (lx *midnightLexer) emit(typ midnightTokenType, value string, line int) {
	lx.tokens = append(lx.tokens, midnightToken{Type: typ, Value: value, Line: line})
}

func isMidnightWordHead(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z'
}

func isMidnightWordBody(c byte) bool { return isMidnightWordHead(c) || isMidnightDigit(c) }

func isMidnightDigit(c byte) bool { return '0' <= c && c <= '9' }

func isMidnightHex(c byte) bool {
	return isMidnightDigit(c) || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

func midnightTokenize(text, source string, opts MidnightOptions) ([]midnightToken, error) {
	return midnightLex(&midnightLexer{source: source, text: text, line: 1, markup: opts.Markup})
}

// MidnightRawTokens splits Midnight source into tokens as written, including
// comments. Strings (including any interpolations) are a single token.
func MidnightRawTokens(text, source string) ([]MidnightRawToken, error) {
	return MidnightRawTokensWithOptions(text, source, MidnightOptions{})
}

// MidnightRawTokensWithOptions is like MidnightRawTokens, but w/ experimental
// extensions. Markup elements are a single token.
func MidnightRawTokensWithOptions(text, source string, opts MidnightOptions) ([]MidnightRawToken, error) {
	raw := []MidnightRawToken{}
	if _, err := midnightLex(&midnightLexer{source: source, text: text, line: 1, raw: &raw, markup: opts.Markup}); err != nil {
		return nil, err
	}
	return raw, nil
}

func midnightLex(lx *midnightLexer) (tokens []midnightToken, err error) {
	text := lx.text

	defer func() {
		if e := recover(); e != nil {
			if perr, ok := e.(*Error); ok {
				err = perr
				return
			}
			panic(e)
		}
	}()

	if strings.HasPrefix(text, "#!") {
		for !lx.eof() && lx.text[lx.pos] != '\n' {
			lx.pos++
		}
		if lx.raw != nil {
			*lx.raw = append(*lx.raw, MidnightRawToken{Kind: RawComment, Text: text[:lx.pos], End: lx.pos, Line: 1, EndLine: 1})
		}
	}

	for !lx.eof() {
		lx.token()
	}

	lx.emit(midnightEOF, "", lx.line)
	return lx.tokens, nil
}

func (lx *midnightLexer) token() {
	if lx.raw != nil && lx.rawDepth == 0 {
		start, line := lx.pos, lx.line
		lx.rawDepth++
		defer func() {
			lx.rawDepth--
			lx.recordRaw(start, line)
		}()
	}

	c := lx.text[lx.pos]

	switch {
	case c == '\n':
		lx.pos++
		lx.line++
	case c == ' ' || c == '\t' || c == '\r':
		lx.pos++
	case isMidnightWordHead(c):
		start := lx.pos
		for !lx.eof() && isMidnightWordBody(lx.text[lx.pos]) {
			lx.pos++
		}
		lx.emit(midnightWord, lx.text[start:lx.pos], lx.line)
	case c == '\'':
		lx.singleQuoteString()
	case c == '"':
		lx.doubleQuoteString()
	case c == '-' && lx.peek(1) == '-':
		lx.comment()
	case c == '0' && (lx.peek(1) == 'x' || lx.peek(1) == 'X'):
		lx.hex()
	case c == '0' && (lx.peek(1) == 'b' || lx.peek(1) == 'B'):
		lx.binary()
	case isMidnightDigit(c) || c == '.' && isMidnightDigit(lx.peek(1)):
		lx.decimal()
	case c == '[' && (lx.peek(1) == '[' || lx.peek(1) == '='):
		lx.blockString()
	case c == '<' && lx.markup && lx.markupStart():
		lx.markupElement()
	default:
		for _, n := range []int{3, 2} {
			if lx.pos+n <= len(lx.text) && midnightSymbols[lx.text[lx.pos:lx.pos+n]] {
				lx.emit(midnightSymbol, lx.text[lx.pos:lx.pos+n], lx.line)
				lx.pos += n
				return
			}
		}
		lx.emit(midnightSymbol, string(c), lx.line)
		lx.pos++
	}
}

func (lx *midnightLexer) recordRaw(start, line int) {
	text := lx.text[start:lx.pos]
	if strings.TrimSpace(text) == "" {
		return
	}

	kind := RawSymbol
	switch c := text[0]; {
	case isMidnightWordHead(c):
		kind = RawWord
	case isMidnightDigit(c) || c == '.' && len(text) > 1 && isMidnightDigit(text[1]):
		kind = RawNumber
	case c == '\'' || c == '"' || c == '[' && len(text) > 1 && (text[1] == '[' || text[1] == '='):
		kind = RawString
	case strings.HasPrefix(text, "--"):
		kind = RawComment
	case lx.markup && len(text) > 1 && text[0] == '<' && (isMidnightWordHead(text[1]) || text[1] == '>'):
		kind = RawMarkup
	}

	*lx.raw = append(*lx.raw, MidnightRawToken{Kind: kind, Text: text, Start: start, End: lx.pos, Line: line, EndLine: lx.line})
}

func (lx *midnightLexer) decimal() {
	start := lx.pos
	isInteger := true

	for isMidnightDigit(lx.peek(0)) {
		lx.pos++
	}

	if lx.peek(0) == '.' && isMidnightDigit(lx.peek(1)) {
		isInteger = false
		lx.pos++
		for isMidnightDigit(lx.peek(0)) {
			lx.pos++
		}
	}

	if c := lx.peek(0); c == 'e' || c == 'E' {
		isInteger = false
		lx.pos++
		if c := lx.peek(0); c == '+' || c == '-' {
			lx.pos++
		}
		if !isMidnightDigit(lx.peek(0)) {
			lx.throw(lx.line, "missing exponent value")
		}
		for isMidnightDigit(lx.peek(0)) {
			lx.pos++
		}
	}

	value := lx.text[start:lx.pos]
	if isInteger {
		// gopher-lua parses integer literals w/ base prefixes, so `010` would be
		// read as octal.
		value = strings.TrimLeft(value, "0")
		if value == "" {
			value = "0"
		}
	}

	lx.emit(midnightNumber, value, lx.line)
}

func (lx *midnightLexer) hex() {
	lx.pos += 2 // 0[xX]

	if !isMidnightHex(lx.peek(0)) && !(lx.peek(0) == '.' && isMidnightHex(lx.peek(1))) {
		lx.throw(lx.line, "malformed hex")
	}

	value := 0.0
	for isMidnightHex(lx.peek(0)) {
		d, _ := strconv.ParseUint(string(lx.peek(0)), 16, 8)
		value = 16*value + float64(d)
		lx.pos++
	}

	if lx.peek(0) == '.' && isMidnightHex(lx.peek(1)) {
		lx.pos++
		for counter := 1.0; isMidnightHex(lx.peek(0)); counter++ {
			d, _ := strconv.ParseUint(string(lx.peek(0)), 16, 8)
			value += float64(d) / math.Pow(16, counter)
			lx.pos++
		}
	}

	if c := lx.peek(0); c == 'p' || c == 'P' {
		lx.pos++
		sign := 1.0
		if c := lx.peek(0); c == '+' || c == '-' {
			if c == '-' {
				sign = -1
			}
			lx.pos++
		}
		if !isMidnightDigit(lx.peek(0)) {
			lx.throw(lx.line, "missing exponent value")
		}
		exponent := 0.0
		for isMidnightDigit(lx.peek(0)) {
			exponent = 10*exponent + float64(lx.peek(0)-'0')
			lx.pos++
		}
		value *= math.Pow(2, sign*exponent)
	}

	lx.emit(midnightNumber, strconv.FormatFloat(value, 'g', -1, 64), lx.line)
}

func (lx *midnightLexer) binary() {
	lx.pos += 2 // 0[bB]

	if c := lx.peek(0); c != '0' && c != '1' {
		lx.throw(lx.line, "malformed binary")
	}

	value := 0.0
	for c := lx.peek(0); c == '0' || c == '1'; c = lx.peek(0) {
		value = 2*value + float64(c-'0')
		lx.pos++
	}

	lx.emit(midnightNumber, strconv.FormatFloat(value, 'g', -1, 64), lx.line)
}

func (lx *midnightLexer) comment() {
	lx.pos += 2 // --

	if lx.peek(0) == '[' {
		equals := 0
		for lx.peek(1+equals) == '=' {
			equals++
		}

		if lx.peek(1+equals) == '[' {
			lx.pos += 2 + equals
			closeQuote := "]" + strings.Repeat("=", equals) + "]"

			for !strings.HasPrefix(lx.text[lx.pos:], closeQuote) {
				if lx.eof() {
					lx.throwEOF("unterminated comment")
				}
				if lx.text[lx.pos] == '\n' {
					lx.line++
				}
				lx.pos++
			}

			lx.pos += len(closeQuote)
			return
		}
	}

	for !lx.eof() && lx.text[lx.pos] != '\n' {
		lx.pos++
	}
}

// escapeSequence decodes a Lua escape sequence, starting after the backslash.
func (lx *midnightLexer) escapeSequence(buf *bytes.Buffer) {
	c := lx.peek(0)

	switch c {
	case 'a':
		buf.WriteByte('\a')
	case 'b':
		buf.WriteByte('\b')
	case 'f':
		buf.WriteByte('\f')
	case 'n':
		buf.WriteByte('\n')
	case 'r':
		buf.WriteByte('\r')
	case 't':
		buf.WriteByte('\t')
	case 'v':
		buf.WriteByte('\v')
	case '\\', '"', '\'':
		buf.WriteByte(c)
	case '\n':
		buf.WriteByte('\n')
		lx.line++
	case 'z':
		lx.pos++
		for !lx.eof() && strings.IndexByte(" \t\r\n\f\v", lx.text[lx.pos]) >= 0 {
			if lx.text[lx.pos] == '\n' {
				lx.line++
			}
			lx.pos++
		}
		return
	case 'x':
		if !isMidnightHex(lx.peek(1)) || !isMidnightHex(lx.peek(2)) {
			lx.throw(lx.line, "escape sequence \\xXX must use exactly 2 hex characters")
		}
		v, _ := strconv.ParseUint(lx.text[lx.pos+1:lx.pos+3], 16, 8)
		buf.WriteByte(byte(v))
		lx.pos += 3
		return
	case 'u':
		lx.pos++
		if lx.peek(0) != '{' {
			lx.throw(lx.line, "missing { in escape sequence \\u{XXX}")
		}
		lx.pos++
		start := lx.pos
		for isMidnightHex(lx.peek(0)) {
			lx.pos++
		}
		if start == lx.pos {
			lx.throw(lx.line, "missing hex in escape sequence \\u{XXX}")
		}
		if lx.peek(0) != '}' {
			lx.throw(lx.line, "missing } in escape sequence \\u{XXX}")
		}
		v, err := strconv.ParseUint(lx.text[start:lx.pos], 16, 32)
		if err != nil || v > utf8.MaxRune {
			lx.throw(lx.line, "UTF-8 value too large")
		}
		var encoded [utf8.UTFMax]byte
		buf.Write(encoded[:utf8.EncodeRune(encoded[:], rune(v))])
		lx.pos++
		return
	default:
		if isMidnightDigit(c) {
			start := lx.pos
			for lx.pos-start < 3 && isMidnightDigit(lx.peek(0)) {
				lx.pos++
			}
			v, _ := strconv.Atoi(lx.text[start:lx.pos])
			if v > 255 {
				lx.throw(lx.line, "decimal escape too large")
			}
			buf.WriteByte(byte(v))
			return
		}
		lx.throw(lx.line, "invalid escape sequence \\%c", c)
	}

	lx.pos++
}

func (lx *midnightLexer) singleQuoteString() {
	lx.emit(midnightSingleQuote, "'", lx.line)
	lx.pos++

	var content bytes.Buffer
	for lx.peek(0) != '\'' {
		switch c := lx.peek(0); {
		case lx.eof() || c == '\n':
			lx.throw(lx.line, "unterminated string")
		case c == '\\':
			lx.pos++
			lx.escapeSequence(&content)
		default:
			content.WriteByte(c)
			lx.pos++
		}
	}

	if content.Len() > 0 {
		lx.emit(midnightStringContent, content.String(), lx.line)
	}

	lx.emit(midnightSingleQuote, "'", lx.line)
	lx.pos++
}

func (lx *midnightLexer) doubleQuoteString() {
	lx.emit(midnightDoubleQuote, "\"", lx.line)
	lx.pos++

	var content bytes.Buffer
	contentLine := lx.line

	flush := func() {
		if content.Len() > 0 {
			lx.emit(midnightStringContent, content.String(), contentLine)
			content.Reset()
		}
		contentLine = lx.line
	}

	for lx.peek(0) != '"' {
		switch c := lx.peek(0); {
		case lx.eof() || c == '\n':
			lx.throw(lx.line, "unterminated string")
		case c == '\\':
			lx.pos++
			if c := lx.peek(0); c == '{' || c == '}' {
				content.WriteByte(c)
				lx.pos++
			} else {
				lx.escapeSequence(&content)
			}
		case c == '{':
			flush()
			lx.interpolation()
			contentLine = lx.line
		default:
			content.WriteByte(c)
			lx.pos++
		}
	}

	flush()
	lx.emit(midnightDoubleQuote, "\"", lx.line)
	lx.pos++
}

func (lx *midnightLexer) blockString() {
	lx.pos++ // [

	equals := 0
	for lx.peek(0) == '=' {
		equals++
		lx.pos++
	}

	if lx.peek(0) != '[' {
		lx.throw(lx.line, "unterminated block string opening")
	}
	lx.pos++

	openQuote := "[" + strings.Repeat("=", equals) + "["
	closeQuote := "]" + strings.Repeat("=", equals) + "]"
	lx.emit(midnightBlockString, openQuote, lx.line)

	// Lua skips a newline directly following the opening quote.
	if strings.HasPrefix(lx.text[lx.pos:], "\r\n") {
		lx.pos += 2
		lx.line++
	} else if lx.peek(0) == '\n' {
		lx.pos++
		lx.line++
	}

	var content bytes.Buffer
	contentLine := lx.line

	flush := func() {
		if content.Len() > 0 {
			lx.emit(midnightStringContent, content.String(), contentLine)
			content.Reset()
		}
		contentLine = lx.line
	}

	for !strings.HasPrefix(lx.text[lx.pos:], closeQuote) {
		switch c := lx.peek(0); {
		case lx.eof():
			lx.throwEOF("unterminated block string")
		case c == '\n':
			content.WriteByte(c)
			lx.pos++
			lx.line++
		case c == '\\':
			lx.pos++
			if c := lx.peek(0); c == '{' || c == '}' {
				content.WriteByte(c)
				lx.pos++
			} else {
				content.WriteByte('\\')
			}
		case c == '{':
			flush()
			lx.interpolation()
			contentLine = lx.line
		default:
			content.WriteByte(c)
			lx.pos++
		}
	}

	flush()
	lx.emit(midnightBlockString, closeQuote, lx.line)
	lx.pos += len(closeQuote)
}

func (lx *midnightLexer) interpolation() {
	lx.emit(midnightInterpolation, "{", lx.line)
	lx.pos++

	braceDepth := 0

	for lx.peek(0) != '}' || braceDepth > 0 {
		switch {
		case lx.eof():
			lx.throwEOF("unterminated interpolation")
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

	lx.emit(midnightInterpolation, "}", lx.line)
	lx.pos++
}
