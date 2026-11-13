package parse

import "sort"

// Highlighting: the lexer (w/ hl set) records what each part of the source is,
// so tools (ex. a docs site's code blocks) color code the way the compiler
// reads it, markup included, instead of keeping a second lexer in sync.

// Highlight kinds (HighlightSpan.Kind).
const (
	HlPlain    = "plain"    // whitespace, and whatever isn't any of these
	HlKeyword  = "keyword"  // ex. local, if, return
	HlConstant = "constant" // true, false, nil
	HlWord     = "word"     // names
	HlNumber   = "number"
	HlString   = "string"  // a whole string literal (delimiters included)
	HlInterp   = "interp"  // the `{` / `}` around an interpolation
	HlFormat   = "format"  // `:%.2f` in `"{x:%.2f}"`
	HlComment  = "comment" // also a leading `#!` line
	HlSymbol   = "symbol"  // operators and punctuation
	HlMarkup   = "markup"  // markup syntax: `<`, `</`, `>`, `/>`, `=`, `{`, `}`
	HlTag      = "tag"     // markup tag name
	HlAttr     = "attr"    // markup attribute name
	HlText     = "text"    // markup text children
	HlRaw      = "raw"     // content of <script> / <style>
)

// HighlightSpan is a part of the source: text[Start:End].
type HighlightSpan struct {
	Kind       string
	Start, End int
}

// MidnightHighlight splits Midnight source into spans that cover all of it,
// in order. On a lex error (ex. an unterminated string in a snippet), it
// still returns spans: the ones up to the error, then the rest as plain.
func MidnightHighlight(text string) ([]HighlightSpan, error) {
	var hl []HighlightSpan
	_, err := midnightLex(&midnightLexer{source: "<highlight>", text: text, line: 1, hl: &hl})
	return flattenHighlight(text, hl), err
}

// flattenHighlight turns the (nested) recorded spans into flat ones: each byte
// gets the kind of the innermost span around it (shorter spans are painted
// over longer ones; for the same length, the later recorded one, which is the
// inner one, see begin).
func flattenHighlight(text string, spans []HighlightSpan) []HighlightSpan {
	sort.SliceStable(spans, func(i, j int) bool {
		return spans[i].End-spans[i].Start > spans[j].End-spans[j].Start
	})
	paint := make([]int, len(text))
	for i := range paint {
		paint[i] = -1
	}
	for i, s := range spans {
		for b := max(s.Start, 0); b < min(s.End, len(text)); b++ {
			paint[b] = i
		}
	}

	kind := func(b int) string {
		if paint[b] < 0 {
			return HlPlain
		}
		return spans[paint[b]].Kind
	}

	var out []HighlightSpan
	for b := 0; b < len(text); {
		// a run of the same span (two tokens of the same kind stay separate,
		// ex. `a b`, so callers see each token)
		start, p, k := b, paint[b], kind(b)
		for b < len(text) && paint[b] == p && kind(b) == k {
			b++
		}
		if n := len(out); n > 0 && k == HlPlain && out[n-1].Kind == HlPlain {
			out[n-1].End = b
			continue
		}
		out = append(out, HighlightSpan{Kind: k, Start: start, End: b})
	}
	return out
}

// begin records a span of kind starting at the current position, ended by
// end(i). Recording it before what's inside of it keeps it under its insides
// (see flattenHighlight).
func (lx *midnightLexer) begin(kind string) int {
	if lx.hl == nil {
		return -1
	}
	*lx.hl = append(*lx.hl, HighlightSpan{Kind: kind, Start: lx.pos, End: lx.pos})
	return len(*lx.hl) - 1
}

func (lx *midnightLexer) end(i int) {
	if i >= 0 {
		(*lx.hl)[i].End = lx.pos
	}
}

// mark records text[lx.pos:lx.pos+n] (call it before advancing past it).
func (lx *midnightLexer) mark(kind string, n int) {
	if lx.hl != nil {
		*lx.hl = append(*lx.hl, HighlightSpan{Kind: kind, Start: lx.pos, End: lx.pos + n})
	}
}
