package luagen

import (
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Literal returns v as a literal that's valid as both Lua and Midnight
// source, ex. for a host that hands values to scripts as code (a template):
//
//		Literal(map[string]any{"title": "Reference", "tags": []any{"a", 1}})
//		// { tags = { 'a', 1 }, title = 'Reference' }
//
//	  - nil is nil, bools are true / false, and numbers are integers when
//	    they're whole (NaN is 0/0, infinities are math.huge / -math.huge).
//	  - Strings are single quoted (double quoted strings interpolate `{...}` in
//	    Midnight), w/ control characters escaped.
//	  - Slices and arrays are lists. Maps are tables, w/ their keys sorted (so
//	    the same value always gives the same code), as `name = ...` when the key
//	    is a name in both languages, else `['key'] = ...`. nil values are left
//	    out, since a table can't hold them.
//	  - Pointers and interfaces are followed. Anything else (ex. functions,
//	    channels, structs) is an error.
func Literal(v any) (string, error) {
	var b strings.Builder
	if err := writeLiteral(&b, reflect.ValueOf(v), 0); err != nil {
		return "", err
	}
	return b.String(), nil
}

// (deep enough for any real data, shallow enough to stop a cycle)
const maxLiteralDepth = 100

func writeLiteral(b *strings.Builder, v reflect.Value, depth int) error {
	if depth > maxLiteralDepth {
		return fmt.Errorf("luagen.Literal: nested more than %d deep (a cycle?)", maxLiteralDepth)
	}
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			b.WriteString("nil")
			return nil
		}
		v = v.Elem()
	}
	if !v.IsValid() {
		b.WriteString("nil")
		return nil
	}

	switch v.Kind() {
	case reflect.Bool:
		b.WriteString(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		b.WriteString(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		b.WriteString(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		b.WriteString(numberLiteral(v.Float()))
	case reflect.String:
		b.WriteString(StringLiteral(v.String()))
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice && v.IsNil() {
			b.WriteString("{}")
			return nil
		}
		if v.Len() == 0 {
			b.WriteString("{}")
			return nil
		}
		b.WriteString("{ ")
		for i := 0; i < v.Len(); i++ {
			if i > 0 {
				b.WriteString(", ")
			}
			if err := writeLiteral(b, v.Index(i), depth+1); err != nil {
				return err
			}
		}
		b.WriteString(" }")
	case reflect.Map:
		return writeMapLiteral(b, v, depth)
	default:
		return fmt.Errorf("luagen.Literal: can't write a %s as a literal", v.Type())
	}
	return nil
}

func writeMapLiteral(b *strings.Builder, v reflect.Value, depth int) error {
	type entry struct {
		key   string // the key as source (`name` or `[...]`)
		order string // what entries are sorted by
		value reflect.Value
	}
	var entries []entry
	iter := v.MapRange()
	for iter.Next() {
		value := iter.Value()
		if isNilValue(value) {
			continue
		}
		key, order, err := keyLiteral(iter.Key())
		if err != nil {
			return err
		}
		entries = append(entries, entry{key, order, value})
	}
	if len(entries) == 0 {
		b.WriteString("{}")
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].order < entries[j].order })

	b.WriteString("{ ")
	for i, e := range entries {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(e.key)
		b.WriteString(" = ")
		if err := writeLiteral(b, e.value, depth+1); err != nil {
			return err
		}
	}
	b.WriteString(" }")
	return nil
}

// keyLiteral returns a map key as table constructor source, and a string to
// sort by.
func keyLiteral(k reflect.Value) (key, order string, err error) {
	for k.Kind() == reflect.Interface && !k.IsNil() {
		k = k.Elem()
	}
	switch k.Kind() {
	case reflect.String:
		s := k.String()
		if isLiteralName(s) {
			return s, s, nil
		}
		return "[" + StringLiteral(s) + "]", s, nil
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		var lit strings.Builder
		if err := writeLiteral(&lit, k, 0); err != nil {
			return "", "", err
		}
		return "[" + lit.String() + "]", lit.String(), nil
	}
	return "", "", fmt.Errorf("luagen.Literal: can't write a %s map key", k.Type())
}

func isNilValue(v reflect.Value) bool {
	for v.IsValid() && (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) {
		if v.IsNil() {
			return true
		}
		v = v.Elem()
	}
	return !v.IsValid()
}

// literalKeywords are the reserved words of Lua and of Midnight, which can't
// be a table key w/o brackets in one or the other. (Midnight's contextual
// keywords, ex. `match`, are names there.)
var literalKeywords = map[string]bool{
	"and": true, "break": true, "continue": true, "do": true, "else": true,
	"elseif": true, "end": true, "false": true, "for": true, "function": true,
	"global": true, "goto": true, "if": true, "in": true, "local": true,
	"module": true, "nil": true, "not": true, "or": true, "repeat": true,
	"return": true, "then": true, "true": true, "until": true, "while": true,
}

func isLiteralName(s string) bool {
	if s == "" || literalKeywords[s] {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '_' || 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || i > 0 && '0' <= c && c <= '9' {
			continue
		}
		return false
	}
	return true
}

func numberLiteral(f float64) string {
	switch {
	case math.IsNaN(f):
		return "(0/0)"
	case math.IsInf(f, 1):
		return "math.huge"
	case math.IsInf(f, -1):
		return "-math.huge"
	case f == math.Trunc(f) && math.Abs(f) < 1e15:
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// StringLiteral returns s as a single quoted string literal, valid as both
// Lua and Midnight (single quoted strings don't interpolate in Midnight).
// Bytes are kept as is (so UTF-8 stays readable), except quotes, backslashes,
// and control characters, which are escaped.
func StringLiteral(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('\'')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\'':
			b.WriteString(`\'`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 || c == 0x7f {
				// (3 digits, so a digit after it isn't read as part of it)
				fmt.Fprintf(&b, `\%03d`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('\'')
	return b.String()
}
