package pm

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"sync"
)

// Faster ways to run a Lua pattern, picked when it's compiled:
//
//   - literal: a pattern that's only plain characters (ex. `<a `,
//     `news%.ycombinator%.com`) is a byte search (bytes.Index)
//   - set: a pattern that's one class (ex. `[&<>"']`, `%s`, `[^%w_]`) is a
//     scan for the first byte in it (a table of the bytes it matches, made
//     w/ the class itself, so it matches exactly what the VM does)
//   - regexp: most other patterns translate to an equivalent Go regexp
//     (RE2's leftmost-first matching w/ greedy / lazy repeats is what the
//     backtracking VM does, as Lua patterns have no alternation)
//
// Anything that doesn't translate exactly (%b, back references, non-ASCII
// pattern bytes, %A etc. inside a set, ...) runs on the VM. Go's regexp works
// on UTF-8 runes, Lua patterns on bytes: they only differ for classes that
// match bytes >= 0x80 (`.`, `[^...]`, %A, %S, ...), so patterns w/ those
// ("wide" ones) use the VM on subjects that have such bytes.

// program is a compiled pattern.
type program struct {
	pat   *seqPattern
	insts []inst

	lit  []byte         // the pattern's bytes, if it's literal
	set  *[256]bool     // the bytes it matches, if it's one class
	re   *regexp.Regexp // the pattern as a regexp, if it translates
	wide bool           // re has classes that can match bytes >= 0x80
	pos  []bool         // per capture: is it a position capture `()`
}

const maxCachedPrograms = 4096

var programs = struct {
	sync.RWMutex
	m map[string]*program
}{m: map[string]*program{}}

// getProgram compiles (or gets the cached) pattern p. Panics w/ *Error for
// invalid patterns, like parsePattern.
func getProgram(p string) *program {
	programs.RLock()
	prog, ok := programs.m[p]
	programs.RUnlock()
	if ok {
		return prog
	}
	prog = compileProgram(p)
	programs.Lock()
	if len(programs.m) >= maxCachedPrograms {
		// (patterns are mostly constants: only built ones fill this up)
		programs.m = map[string]*program{}
	}
	programs.m[p] = prog
	programs.Unlock()
	return prog
}

func compileProgram(p string) *program {
	pat := parsePattern(newScanner([]byte(p)), true)
	prog := &program{pat: pat, insts: compilePattern(pat)}
	if lit, ok := literal(pat); ok {
		prog.lit = lit
		return prog
	}
	if set, ok := byteSet(pat); ok {
		prog.set = set
		return prog
	}
	t := &translator{}
	if t.seq(pat) {
		expr := "(?s)" + t.b.String()
		if pat.MustHead {
			expr = "^" + expr
		}
		if pat.MustTail {
			expr += `\z`
		}
		if re, err := regexp.Compile(expr); err == nil {
			prog.re, prog.wide, prog.pos = re, t.wide, t.pos
		}
	}
	return prog
}

// literal returns the bytes of a pattern that's only plain characters.
func literal(pat *seqPattern) ([]byte, bool) {
	if pat.MustHead || pat.MustTail || len(pat.Patterns) == 0 {
		return nil, false
	}
	lit := make([]byte, 0, len(pat.Patterns))
	for _, p := range pat.Patterns {
		sp, ok := p.(*singlePattern)
		if !ok {
			return nil, false
		}
		ch, ok := literalChar(sp.Class)
		if !ok {
			return nil, false
		}
		lit = append(lit, byte(ch))
	}
	return lit, true
}

// byteSet returns the bytes a pattern that's one class (w/o anchors or
// repeats) matches.
func byteSet(pat *seqPattern) (*[256]bool, bool) {
	if pat.MustHead || pat.MustTail || len(pat.Patterns) != 1 {
		return nil, false
	}
	sp, ok := pat.Patterns[0].(*singlePattern)
	if !ok {
		return nil, false
	}
	set := &[256]bool{}
	for b := range set {
		set[b] = sp.Class.Matches(b)
	}
	return set, true
}

// literalChar returns the one byte a class matches: `x`, or an escaped
// non-class like `%.` / `%%`.
func literalChar(c class) (int, bool) {
	switch c := c.(type) {
	case *charClass:
		return c.Ch, c.Ch >= 0 && c.Ch <= 0xff
	case *singleClass:
		if _, isClass := classRanges[lower(c.Class)]; isClass {
			return 0, false
		}
		return c.Class, c.Class >= 0 && c.Class <= 0xff
	}
	return 0, false
}

func lower(ch int) int {
	if 'A' <= ch && ch <= 'Z' {
		return ch + 'a' - 'A'
	}
	return ch
}

// classRanges are %a etc. as regexp ranges (what singleClass matches).
var classRanges = map[int]string{
	'a': `A-Za-z`,
	'c': `\x00-\x1f\x7f`,
	'd': `0-9`,
	'l': `a-z`,
	'p': `\x21-\x2f\x3a-\x40\x5b-\x60\x7b-\x7e`,
	's': `\x09-\x0d\x20`,
	'u': `A-Z`,
	'w': `0-9A-Za-z`,
	'x': `0-9A-Fa-f`,
	'z': `\x00`,
}

// translator writes a parsed pattern as a regexp.
type translator struct {
	b    strings.Builder
	wide bool
	pos  []bool
}

func (t *translator) seq(pat *seqPattern) bool {
	for _, p := range pat.Patterns {
		switch p := p.(type) {
		case *singlePattern:
			if !t.class(p.Class) {
				return false
			}
		case *repeatPattern:
			if !t.class(p.Class) {
				return false
			}
			switch p.Type {
			case '*':
				t.b.WriteString("*")
			case '+':
				t.b.WriteString("+")
			case '-':
				t.b.WriteString("*?")
			case '?':
				t.b.WriteString("?")
			default:
				return false
			}
		case *posCapPattern:
			t.pos = append(t.pos, true)
			t.b.WriteString("()")
		case *capPattern:
			inner, ok := p.Pattern.(*seqPattern)
			if !ok {
				return false
			}
			t.pos = append(t.pos, false)
			t.b.WriteString("(")
			if !t.seq(inner) {
				return false
			}
			t.b.WriteString(")")
		default: // %b, %1, ...
			return false
		}
	}
	return true
}

// class writes one class as a single regexp atom (so a repeat applies to all
// of it).
func (t *translator) class(c class) bool {
	switch c := c.(type) {
	case *dotClass:
		t.wide = true
		t.b.WriteString(".")
		return true
	case *singleClass:
		ranges, isClass := classRanges[lower(c.Class)]
		if !isClass {
			return t.char(c.Class)
		}
		if lower(c.Class) != c.Class { // %A, %S, ...: the complement
			t.wide = true
			t.b.WriteString("[^" + ranges + "]")
		} else {
			t.b.WriteString("[" + ranges + "]")
		}
		return true
	case *charClass:
		return t.char(c.Ch)
	case *setClass:
		if len(c.Classes) == 0 {
			return false
		}
		var set strings.Builder
		for _, item := range c.Classes {
			if !setItem(&set, item) {
				return false
			}
		}
		if c.IsNot {
			t.wide = true
			t.b.WriteString("[^" + set.String() + "]")
		} else {
			t.b.WriteString("[" + set.String() + "]")
		}
		return true
	}
	return false
}

// char writes an ASCII character (non-ASCII pattern bytes run on the VM:
// regexp would read them as runes).
func (t *translator) char(ch int) bool {
	if ch < 0 || ch > 0x7f {
		return false
	}
	t.b.WriteString(regexpChar(ch))
	return true
}

func regexpChar(ch int) string {
	if 'a' <= ch && ch <= 'z' || 'A' <= ch && ch <= 'Z' || '0' <= ch && ch <= '9' {
		return string(rune(ch))
	}
	return fmt.Sprintf(`\x%02x`, ch)
}

// setItem writes a class inside a regexp set.
func setItem(set *strings.Builder, c class) bool {
	switch c := c.(type) {
	case *charClass:
		if c.Ch < 0 || c.Ch > 0x7f {
			return false
		}
		set.WriteString(regexpChar(c.Ch))
		return true
	case *singleClass:
		ranges, isClass := classRanges[lower(c.Class)]
		if !isClass {
			if c.Class < 0 || c.Class > 0x7f {
				return false
			}
			set.WriteString(regexpChar(c.Class))
			return true
		}
		if lower(c.Class) != c.Class {
			return false // a complement inside a set
		}
		set.WriteString(ranges)
		return true
	case *rangeClass:
		begin, ok1 := c.Begin.(*charClass)
		end, ok2 := c.End.(*charClass)
		if !ok1 || !ok2 || begin.Ch < 0 || end.Ch > 0x7f || begin.Ch > end.Ch {
			return false
		}
		set.WriteString(regexpChar(begin.Ch) + "-" + regexpChar(end.Ch))
		return true
	}
	return false
}

func hasHighByte(src []byte, offset int) bool {
	if offset >= len(src) {
		return false
	}
	for _, b := range src[offset:] {
		if b >= 0x80 {
			return true
		}
	}
	return false
}

// findLiteral is findVM for a literal pattern.
func (prog *program) findLiteral(src []byte, offset, limit int) []*MatchData {
	matches := []*MatchData{}
	for sp := offset; sp <= len(src); {
		i := bytes.Index(src[sp:], prog.lit)
		if i < 0 {
			break
		}
		start := sp + i
		if !limitAt(&limit, sp, start) {
			break
		}
		matches = append(matches, &MatchData{[]uint32{uint32(start) << 1, uint32(start+len(prog.lit)) << 1}})
		sp = start + len(prog.lit)
		if len(matches) == limit {
			break
		}
	}
	return matches
}

// findSet is findVM for a one class pattern: each match is the next byte in
// its set.
func (prog *program) findSet(src []byte, offset, limit int) []*MatchData {
	matches := []*MatchData{}
	for sp := offset; sp < len(src); {
		start := sp
		for start < len(src) && !prog.set[src[start]] {
			start++
		}
		if start == len(src) || !limitAt(&limit, sp, start) {
			break
		}
		matches = append(matches, &MatchData{[]uint32{uint32(start) << 1, uint32(start+1) << 1}})
		sp = start + 1
		if len(matches) == limit {
			break
		}
	}
	return matches
}

// findRegexp is findVM w/ the pattern's regexp: the next match from sp is
// the leftmost one, which the VM finds by trying each position.
func (prog *program) findRegexp(src []byte, offset, limit int) []*MatchData {
	if offset > len(src) {
		return []*MatchData{}
	}
	matches := []*MatchData{}
	for sp := offset; sp <= len(src); {
		loc := prog.re.FindSubmatchIndex(src[sp:])
		if loc == nil {
			break
		}
		if !limitAt(&limit, sp, sp+loc[0]) {
			break
		}
		matches = append(matches, prog.matchData(sp, loc))
		start, end := sp+loc[0], sp+loc[1]
		sp = start + 1
		if sp < end {
			sp = end
		}
		if len(matches) == limit || prog.pat.MustHead {
			break
		}
	}
	return matches
}

// matchData converts a regexp match (of src[sp:]) to the VM's MatchData.
func (prog *program) matchData(sp int, loc []int) *MatchData {
	md := &MatchData{make([]uint32, len(loc))}
	md.captures[0] = uint32(sp+loc[0]) << 1
	md.captures[1] = uint32(sp+loc[1]) << 1
	for i, isPos := range prog.pos {
		s, e := sp+loc[2+2*i], sp+loc[3+2*i]
		if isPos {
			// (1-based, like the VM's position captures)
			md.captures[2+2*i] = uint32(s+1)<<1 | 1
			md.captures[3+2*i] = uint32(s+1)<<1 | 1
		} else {
			md.captures[2+2*i] = uint32(s) << 1
			md.captures[3+2*i] = uint32(e) << 1
		}
	}
	return md
}

// limitAt is the VM's limit check for a match found at start, searching from
// sp: it checks the limit after each position it tries, so w/ a limit of 0 it
// stops at the first one only if that isn't a match (and has no limit
// otherwise).
func limitAt(limit *int, sp, start int) bool {
	if *limit != 0 {
		return true
	}
	if start != sp {
		return false
	}
	*limit = -1
	return true
}
