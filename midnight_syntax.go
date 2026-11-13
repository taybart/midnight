package lua

// midnight: `midnight.syntax`, the compiler's view of midnight source for
// tools, ex. a syntax highlighter that colors code the way the compiler reads
// it instead of keeping a second lexer in sync (see parse/midnight_highlight.go).

import "github.com/taybart/midnight/parse"

// SyntaxLibName is the module name of the syntax library (`require('midnight.syntax')`).
const SyntaxLibName = "midnight.syntax"

// OpenSyntax registers the syntax library in package.preload.
func OpenSyntax(L *LState) int {
	L.PreloadModule(SyntaxLibName, func(L *LState) int {
		L.Push(L.SetFuncs(L.NewTable(), map[string]LGFunction{"tokens": syntaxTokens}))
		return 1
	})
	return 0
}

// syntaxTokens(src) returns src split into a list of `{ kind, text }` that
// covers all of it, in order (kinds are the parse.Hl* constants: plain,
// keyword, constant, word, number, string, interp, format, comment, symbol,
// markup, tag, attr, text, raw). On a lex error it returns the tokens up to
// it (the rest is one plain token) and the error message.
func syntaxTokens(L *LState) int {
	src := L.CheckString(1)
	spans, err := parse.MidnightHighlight(src)
	list := L.CreateTable(len(spans), 0)
	for _, s := range spans {
		tok := L.CreateTable(0, 2)
		tok.RawSetString("kind", LString(s.Kind))
		tok.RawSetString("text", LString(src[s.Start:s.End]))
		list.Append(tok)
	}
	L.Push(list)
	if err != nil {
		L.Push(LString(err.Error()))
		return 2
	}
	return 1
}
