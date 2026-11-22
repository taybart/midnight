package mngen

import "strings"

// The Lua AST has no comments, and doesn't record where some closing tokens
// are (ex. a table's `}`). scan collects them from the source, so the output
// keeps the source's comments and layout.

type comment struct {
	line, endLine int
	text          string
}

// opener is a `(` or `{` and the line of its matching `)` / `}`.
type opener struct {
	line, closeLine int
}

type scanned struct {
	comments []comment
	// Lines of `else` keywords, in order.
	elses []int
	// `(` / `{` by the line they're on, in source order.
	parens, braces map[int][]*opener
	// Lines that multiline strings start on, and whether the string starts
	// w/ a skipped newline (`[[` at the end of the line).
	multiline map[int]bool
}

// scan scans Lua source (which must be valid, it's already been parsed).
func scan(text string) *scanned {
	s := &scanned{parens: map[int][]*opener{}, braces: map[int][]*opener{}, multiline: map[int]bool{}}
	line := 1
	pos := 0
	var stack []*opener

	peek := func(n int) byte {
		if pos+n < len(text) {
			return text[pos+n]
		}
		return 0
	}

	// longBracket returns the number of `=` in a long bracket at pos (`[[`,
	// `[==[`), or -1.
	longBracket := func(at int) int {
		if at >= len(text) || text[at] != '[' {
			return -1
		}
		i := at + 1
		for i < len(text) && text[i] == '=' {
			i++
		}
		if i < len(text) && text[i] == '[' {
			return i - at - 1
		}
		return -1
	}

	// skipLong skips a long string / comment body starting at its opening
	// bracket (w/ `level` equals).
	skipLong := func(level int) {
		pos += level + 2
		closeQuote := "]" + strings.Repeat("=", level) + "]"
		end := strings.Index(text[pos:], closeQuote)
		if end < 0 {
			end = len(text) - pos
		} else {
			end += len(closeQuote)
		}
		line += strings.Count(text[pos:pos+end], "\n")
		pos += end
	}

	// A shebang
	if strings.HasPrefix(text, "#!") {
		end := strings.IndexByte(text, '\n')
		if end < 0 {
			end = len(text)
		}
		s.comments = append(s.comments, comment{line: 1, endLine: 1, text: strings.TrimRight(text[:end], "\r")})
		pos = end
	}

	for pos < len(text) {
		c := text[pos]
		switch {
		case c == '\n':
			line++
			pos++
		case c == '-' && peek(1) == '-':
			start, startLine := pos, line
			if level := longBracket(pos + 2); level >= 0 {
				pos += 2
				skipLong(level)
			} else {
				for pos < len(text) && text[pos] != '\n' {
					pos++
				}
			}
			s.comments = append(s.comments, comment{line: startLine, endLine: line, text: strings.TrimRight(text[start:pos], "\r")})
		case c == '\'' || c == '"':
			startLine := line
			pos++
			for pos < len(text) && text[pos] != c {
				if text[pos] == '\\' {
					pos++
				}
				if pos < len(text) && text[pos] == '\n' {
					line++
				}
				pos++
			}
			pos++
			if line > startLine {
				s.multiline[startLine] = false
			}
		case c == '[' && longBracket(pos) >= 0:
			startLine, level := line, longBracket(pos)
			leadingNewline := pos+level+2 < len(text) && text[pos+level+2] == '\n'
			skipLong(level)
			if line > startLine {
				s.multiline[startLine] = leadingNewline
			}
		case c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9':
			// (numbers too, so `1e5` isn't read as a name)
			start := pos
			for pos < len(text) && isWordByte(text[pos]) {
				pos++
			}
			if text[start:pos] == "else" {
				s.elses = append(s.elses, line)
			}
		case c == '(' || c == '{':
			o := &opener{line: line}
			if c == '(' {
				s.parens[line] = append(s.parens[line], o)
			} else {
				s.braces[line] = append(s.braces[line], o)
			}
			stack = append(stack, o)
			pos++
		case c == ')' || c == '}':
			if len(stack) > 0 {
				stack[len(stack)-1].closeLine = line
				stack = stack[:len(stack)-1]
			}
			pos++
		default:
			pos++
		}
	}

	return s
}

func isWordByte(c byte) bool {
	return c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9'
}
