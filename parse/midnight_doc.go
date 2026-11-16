package parse

import "strings"

// isDocComment reports whether a comment (ex. from a RawComment token) is a
// lua-language-server doc comment: it starts with `---` (three dashes) but not
// `----` (a section separator). Block comments start with `--[` and so are
// never doc comments. This mirrors lua-language-server, where annotations
// (`---@class`, `---@param`, ...) and their description lines use `---`, a bare
// `---` is a line break, and a normal `--` comment is ignored.
func isDocComment(text string) bool {
	return strings.HasPrefix(text, "---") && !strings.HasPrefix(text, "----")
}

// MidnightDocComments returns the doc comments in Midnight source, keyed by
// line, so a Lua compiler can emit them (ex. lua-language-server annotations)
// in the generated Lua. Non doc comments are omitted.
func MidnightDocComments(text, source string) (map[int]string, error) {
	// Normalize CRLF so a comment's text doesn't keep a trailing `\r`.
	raw, err := MidnightRawTokens(strings.ReplaceAll(text, "\r\n", "\n"), source)
	if err != nil {
		return nil, err
	}
	comments := map[int]string{}
	for _, t := range raw {
		if t.Kind == RawComment && isDocComment(t.Text) {
			comments[t.Line] = t.Text
		}
	}
	return comments, nil
}
