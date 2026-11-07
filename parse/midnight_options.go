package parse

import (
	"fmt"
	"strings"
)

// MidnightOptions enables experimental Midnight extensions. These are midnight only
// (the reference compiler does not support them), so they are all off by
// default. With everything off, the parser accepts exactly the language of the
// reference compiler (ex. `match` is only ever an ordinary name).
type MidnightOptions struct {
	// Match enables (Rust style) `match` statements:
	//
	//	match mode {
	//	  "movie" => { ... }
	//	  "show" | "series" => { ... }
	//	  _ if count > 5 => { ... }
	//	  _ => { ... }
	//	}
	//
	// Patterns are expressions compared w/ `==` (names are values, not
	// bindings). The subject is evaluated once and there is no fallthrough.
	//
	// `match` is a contextual keyword, so it is still usable as a name (ex.
	// `s:match(p)`, `local match = 1`, `match(x)`).
	Match bool

	// Markup enables HTML-like (JSX style) elements, which are lowered to calls of
	// MarkupFactory (see midnight_markup.go):
	//
	//	local page = <Layout title="home">
	//	  <p class="greeting">hello {name}!</p>
	//	</Layout>
	//
	// The runtime library `midnight.markup` provides a factory (`h`) that renders to
	// HTML strings.
	Markup bool
	// MarkupFactory is the (possibly dotted) name of the function elements are
	// lowered to. By default, it's `h` if declared, otherwise the runtime
	// library's `h` (imported automatically).
	MarkupFactory string
}

// MidnightExperiments lists the experiments accepted by Enable.
var MidnightExperiments = []string{"match", "markup[=factory]"}

// Enable turns on experiments by name, given as a comma separated list (ex.
// from a command line flag). `markup=name` also sets MarkupFactory (ex.
// `markup=harmony.h`).
func (o *MidnightOptions) Enable(names string) error {
	for _, name := range strings.Split(names, ",") {
		name, value, hasValue := strings.Cut(strings.TrimSpace(name), "=")
		switch {
		case name == "" && !hasValue:
		case name == "match" && !hasValue:
			o.Match = true
		case name == "markup" || name == "jsx": // jsx: the old (deprecated) name
			o.Markup = true
			if hasValue {
				if !isDottedName(value) {
					return fmt.Errorf("invalid markup factory '%s' (expected a name, ex. h or ui.h)", value)
				}
				o.MarkupFactory = value
			}
		default:
			return fmt.Errorf("unknown midnight experiment '%s' (available: %s)", name, strings.Join(MidnightExperiments, ", "))
		}
	}
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
