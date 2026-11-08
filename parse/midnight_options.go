package parse

import (
	"fmt"
	"strings"
)

// MidnightOptions configures the Midnight parser. The zero value is the
// default.
//
// The language includes (Rust style) `match` statements:
//
//	match mode {
//	  "movie" => { ... }
//	  "show" | "series" => { ... }
//	  _ if count > 5 => { ... }
//	  _ => { ... }
//	}
//
// Patterns are expressions compared w/ `==` (names are values, not bindings).
// The subject is evaluated once and there is no fallthrough. `match` is a
// contextual keyword, so it is still usable as a name (ex. `s:match(p)`,
// `local match = 1`, `match(x)`).
//
// And HTML-like (JSX style) markup elements, which are lowered to calls of
// MarkupFactory (see midnight_markup.go):
//
//	local page = <Layout title="home">
//	  <p class="greeting">hello {name}!</p>
//	</Layout>
//
// The runtime library `midnight.markup` provides a factory (`h`) that renders
// to HTML strings.
type MidnightOptions struct {
	// MarkupFactory is the (possibly dotted) name of the function elements are
	// lowered to. By default, it's `h` if declared, otherwise the runtime
	// library's `h` (imported automatically).
	MarkupFactory string
}

// SetMarkupFactory validates and sets MarkupFactory (ex. from a command line
// flag): a name or a dotted chain of names, ex. `h` or `harmony.ui.h`.
func (o *MidnightOptions) SetMarkupFactory(name string) error {
	if !isDottedName(name) {
		return fmt.Errorf("invalid markup factory '%s' (expected a name, ex. h or ui.h)", name)
	}
	o.MarkupFactory = name
	return nil
}

// isDottedName reports whether s is a name or a dotted chain of names (ex.
// `markup.h`).
func isDottedName(s string) bool {
	for _, part := range strings.Split(s, ".") {
		if part == "" || midnightKeywords[part] || !isMidnightWordHead(part[0]) {
			return false
		}
		for i := 1; i < len(part); i++ {
			if !isMidnightWordBody(part[i]) {
				return false
			}
		}
	}
	return true
}
