package parse

import "strings"

// Code highlighting for tools that color code by class (ex. the docs site's
// code blocks and the playground's editor), so the rules live in one place
// instead of in each tool: Midnight is split by the compiler's lexer (see
// MidnightHighlight), then each token gets a class; Lua (ex. what Midnight
// compiles to) has a small scanner here.

// Highlight classes (CodeSpan.Class). Tools map these to styles, ex.
// `tok-keyword` CSS classes.
const (
	ClassPlain    = ""         // whitespace, names, markup text, ...
	ClassKeyword  = "keyword"  // local, if, return (and `match`, `class`, `const`, ... where they're keywords)
	ClassConst    = "const"    // true, false, nil
	ClassNumber   = "number"   //
	ClassString   = "string"   // a string literal, delimiters included
	ClassInterp   = "interp"   // `{` / `}` around an interpolation, and its format
	ClassComment  = "comment"  //
	ClassPunct    = "punct"    // brackets, `,`, `.`, `:`, `;`, markup syntax
	ClassOperator = "operator" // everything else that's a symbol
	ClassBuiltin  = "builtin"  // standard library names (print, string, ...)
	ClassFunction = "function" // a name that's called (followed by `(`)
	ClassTag      = "tag"      // markup tag name
	ClassAttr     = "attr"     // markup attribute name
)

// CodeSpan is a part of the source, text[Start:End], and its class.
type CodeSpan struct {
	Class      string
	Start, End int
}

// HighlightLangs are the languages HighlightCode knows (w/ aliases).
var HighlightLangs = map[string]string{
	"midnight": "midnight", "mn": "midnight",
	"lua": "lua",
}

// HighlightCode splits text (in lang, see HighlightLangs) into classed spans
// that cover all of it, in order. ok is false for an unknown language. Like
// MidnightHighlight, a lex error isn't fatal: the rest is plain.
func HighlightCode(lang, text string) (spans []CodeSpan, ok bool) {
	switch HighlightLangs[strings.ToLower(lang)] {
	case "midnight":
		return highlightMidnight(text), true
	case "lua":
		return highlightLua(text), true
	}
	return nil, false
}

// highlightBuiltins are the standard library names colored as builtins.
var highlightBuiltins = map[string]bool{
	"print": true, "require": true, "type": true, "tostring": true, "tonumber": true,
	"pairs": true, "ipairs": true, "next": true, "select": true, "error": true,
	"assert": true, "pcall": true, "xpcall": true, "setmetatable": true,
	"getmetatable": true, "rawget": true, "rawset": true, "rawequal": true,
	"rawlen": true, "unpack": true, "string": true, "table": true, "math": true,
	"io": true, "os": true, "utf8": true, "coroutine": true, "bit32": true,
}

// punctuation (the other symbols are operators)
var highlightPunct = map[string]bool{
	"(": true, ")": true, "[": true, "]": true, "{": true, "}": true,
	";": true, ":": true, ",": true, ".": true,
}

var midnightHighlightClasses = map[string]string{
	HlKeyword: ClassKeyword, HlConstant: ClassConst, HlNumber: ClassNumber,
	HlString: ClassString, HlInterp: ClassInterp, HlFormat: ClassInterp,
	HlComment: ClassComment, HlMarkup: ClassPunct, HlTag: ClassTag, HlAttr: ClassAttr,
}

func highlightMidnight(text string) []CodeSpan {
	spans, _ := MidnightHighlight(text)
	tok := func(i int) string { return text[spans[i].Start:spans[i].End] }

	// next returns the token after i, skipping whitespace (and whether that
	// whitespace has a newline), or -1.
	next := func(i int) (int, bool) {
		j, newline := i+1, false
		if j < len(spans) && spans[j].Kind == HlPlain && strings.TrimSpace(tok(j)) == "" {
			newline = strings.Contains(tok(j), "\n")
			j++
		}
		if j >= len(spans) {
			return -1, newline
		}
		return j, newline
	}

	out := make([]CodeSpan, len(spans))
	for i, s := range spans {
		class := midnightHighlightClasses[s.Kind]
		switch s.Kind {
		case HlWord:
			word := tok(i)
			j, newline := next(i)
			nextKind, nextText := "", ""
			if j >= 0 {
				nextKind, nextText = spans[j].Kind, tok(j)
			}
			switch {
			case highlightBuiltins[word]:
				class = ClassBuiltin
			case word == "match" && j >= 0 && nextKind != HlSymbol:
				class = ClassKeyword // (only a keyword before its value)
			case word == "catch" && i > 0 && j >= 0 && (nextKind != HlSymbol || nextText == "{"):
				class = ClassKeyword // (only a keyword before its handler, see isCatch)
			case (word == "class" || word == "static" || word == "goto") && nextKind == HlWord && !newline:
				class = ClassKeyword // (contextual: only before a name, see isClassDeclaration)
			case word == "const" && !newline && (nextKind == HlWord || nextText == "{" || nextText == "["):
				class = ClassKeyword // (contextual: only before what it declares, see isConstDeclaration)
			case nextKind == HlSymbol && nextText == "(":
				class = ClassFunction
			}
		case HlSymbol:
			class = ClassOperator
			if highlightPunct[tok(i)] {
				class = ClassPunct
			}
		}
		out[i] = CodeSpan{Class: class, Start: s.Start, End: s.End}
	}
	return out
}

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true, "end": true,
	"for": true, "function": true, "goto": true, "if": true, "in": true, "local": true,
	"not": true, "or": true, "repeat": true, "return": true, "then": true,
	"until": true, "while": true,
}

// highlightLua scans Lua source. It's for coloring, not parsing: anything it
// doesn't recognize is plain, and unterminated strings / comments run to the
// end of the line (or text, for long ones).
func highlightLua(text string) []CodeSpan {
	var out []CodeSpan
	emit := func(class string, start, end int) {
		if n := len(out); n > 0 && class == ClassPlain && out[n-1].Class == ClassPlain && out[n-1].End == start {
			out[n-1].End = end
			return
		}
		out = append(out, CodeSpan{Class: class, Start: start, End: end})
	}
	isIdentStart := func(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }
	isDigit := func(c byte) bool { return c >= '0' && c <= '9' }
	isIdent := func(c byte) bool { return isIdentStart(c) || isDigit(c) }

	// longBracket returns the end of a long bracket (`[[ ]]`, `[==[ ]==]`)
	// starting at i, or -1 if there isn't one there.
	longBracket := func(i int) int {
		if i >= len(text) || text[i] != '[' {
			return -1
		}
		j := i + 1
		for j < len(text) && text[j] == '=' {
			j++
		}
		if j >= len(text) || text[j] != '[' {
			return -1
		}
		closer := "]" + strings.Repeat("=", j-i-1) + "]"
		if k := strings.Index(text[j+1:], closer); k >= 0 {
			return j + 1 + k + len(closer)
		}
		return len(text)
	}
	lineEnd := func(i int) int {
		if k := strings.IndexByte(text[i:], '\n'); k >= 0 {
			return i + k
		}
		return len(text)
	}

	for i := 0; i < len(text); {
		c := text[i]
		switch {
		case strings.HasPrefix(text[i:], "--"):
			end := longBracket(i + 2)
			if end < 0 {
				end = lineEnd(i)
			}
			emit(ClassComment, i, end)
			i = end
		case c == '"' || c == '\'':
			j := i + 1
			for j < len(text) && text[j] != c && text[j] != '\n' {
				if text[j] == '\\' {
					j++
				}
				j++
			}
			if j < len(text) && text[j] == c {
				j++
			}
			j = min(j, len(text))
			emit(ClassString, i, j)
			i = j
		case c == '[' && longBracket(i) >= 0:
			end := longBracket(i)
			emit(ClassString, i, end)
			i = end
		case isDigit(c) || c == '.' && i+1 < len(text) && isDigit(text[i+1]):
			j := i
			if strings.HasPrefix(text[i:], "0x") || strings.HasPrefix(text[i:], "0X") {
				j += 2
			}
			for j < len(text) && (isIdent(text[j]) || text[j] == '.' ||
				(text[j] == '+' || text[j] == '-') && strings.ContainsRune("eEpP", rune(text[j-1]))) {
				j++
			}
			emit(ClassNumber, i, j)
			i = j
		case isIdentStart(c):
			j := i
			for j < len(text) && isIdent(text[j]) {
				j++
			}
			word := text[i:j]
			k := j
			for k < len(text) && (text[k] == ' ' || text[k] == '\t') {
				k++
			}
			class := ClassPlain
			switch {
			case luaKeywords[word]:
				class = ClassKeyword
			case word == "true" || word == "false" || word == "nil":
				class = ClassConst
			case highlightBuiltins[word]:
				class = ClassBuiltin
			case k < len(text) && text[k] == '(':
				class = ClassFunction
			}
			emit(class, i, j)
			i = j
		case strings.ContainsRune("()[]{};:,.", rune(c)):
			n := 1
			if strings.HasPrefix(text[i:], "::") {
				n = 2
				emit(ClassOperator, i, i+n)
			} else if strings.HasPrefix(text[i:], "..") {
				n = 2
				if strings.HasPrefix(text[i:], "...") {
					n = 3
				}
				emit(ClassOperator, i, i+n)
			} else {
				emit(ClassPunct, i, i+n)
			}
			i += n
		case strings.ContainsRune("+-*/%^#<>=~", rune(c)):
			n := 1
			if i+1 < len(text) && text[i+1] == '=' && strings.ContainsRune("=~<>", rune(c)) {
				n = 2
			}
			emit(ClassOperator, i, i+n)
			i += n
		default:
			emit(ClassPlain, i, i+1)
			i++
		}
	}
	return out
}
