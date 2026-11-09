package pm

import (
	"fmt"
	"math/rand"
	"reflect"
	"regexp"
	"testing"
)

// findBoth runs a pattern w/ Find (literal / regexp / VM) and w/ the VM only.
func findBoth(p string, src []byte, offset, limit int) (fast, vm []*MatchData, fastErr, vmErr error) {
	fast, fastErr = Find(p, src, offset, limit)
	func() {
		defer func() {
			if v := recover(); v != nil {
				vmErr = v.(*Error)
			}
		}()
		vm = compileProgram(p).findVM(src, offset, limit)
	}()
	return
}

func captures(mds []*MatchData) [][]uint32 {
	out := [][]uint32{}
	for _, md := range mds {
		out = append(out, md.captures)
	}
	return out
}

func checkSame(t *testing.T, p string, src []byte, offset, limit int) {
	t.Helper()
	fast, vm, fastErr, vmErr := findBoth(p, src, offset, limit)
	if (fastErr == nil) != (vmErr == nil) || fastErr != nil && fastErr.Error() != vmErr.Error() {
		t.Fatalf("pattern %q on %q: errors differ: fast %v, vm %v", p, src, fastErr, vmErr)
	}
	if !reflect.DeepEqual(captures(fast), captures(vm)) {
		prog := compileProgram(p)
		engine := "vm"
		if prog.lit != nil {
			engine = "literal"
		} else if prog.set != nil {
			engine = "set"
		} else if prog.re != nil {
			engine = "regexp " + prog.re.String()
		}
		t.Fatalf("pattern %q on %q (offset %d, limit %d, %s):\n fast %v\n vm   %v",
			p, src, offset, limit, engine, captures(fast), captures(vm))
	}
}

func TestFastPaths(t *testing.T) {
	cases := []struct{ pat, src string }{
		{"<a ", `x <a href="1"> <a b>`},
		{"news%.ycombinator%.com&#x2F;item%?id=", "see news.ycombinator.com&#x2F;item?id=1 and news.ycombinator.com&#x2F;item?id=2"},
		{"item%?id=(%d+)", "https://news.ycombinator.com/item?id=42"},
		{"^%s*(.-)%s*$", "  trim me  "},
		{"(%w+)=(%w+)", "a=1, b=2, c=3"},
		{"()ll()", "hello"},
		{"%w*", "abc def"},
		{"", "abc"},
		{"x*", "abc"},
		{"[%a_][%w_]*", "local foo_bar = 1"},
		{"[^,]+", "a,b,,c"},
		{"[^,]+", "é,ü,x"},
		{".", "é"},
		{"%S+", "héllo wörld"},
		{"%b()", "f(a(b)c)"},
		{"(a)%1", "aa"},
		{"[]]", "a]b"},
		{"[a-]", "x-a"},
		{"$", "abc"},
		{"a$b", "a$b"},
		{"^", "abc"},
		{"%z", "a\x00b"},
		{"[%z]", "a\x00b"},
		{"%f[%w]%w+", "hello world"},
		{`[&<>"']`, `a < b && c > "d" 'e'`},
		{`[&<>"']`, "nothing to escape"},
		{"%s", "a b\tc\nd"},
		{"[^%w_]", "foo_bar-baz.qux"},
		{"[%c\"\\<>&]", "x\x01y\"z\\<&>"},
		{".", "aé\xff"},
		{"[^a]", "aé\xff"},
		{"%A", "a1é"},
	}
	for _, c := range cases {
		for offset := 0; offset <= len(c.src)+1; offset++ {
			for _, limit := range []int{-1, 0, 1, 2} {
				checkSame(t, c.pat, []byte(c.src), offset, limit)
			}
		}
	}
}

// TestFastPathsRandom compares the fast paths to the VM on random patterns
// and subjects.
func TestFastPathsRandom(t *testing.T) {
	atoms := []string{
		"a", "b", "c", " ", ".", "%a", "%d", "%s", "%w", "%p", "%A", "%S", "%D",
		"%.", "%%", "%-", "[ab]", "[^ab]", "[a-c]", "[%d%s]", "[^%a]", "[%A]",
		"(", ")", "()", "*", "+", "-", "?", "^", "$", "1", "x", ",", "\n",
	}
	subj := []string{"a", "b", "c", " ", ".", "1", "2", "x", ",", "-", "%", "\n", "é", "\xff", "ab", "ba"}
	r := rand.New(rand.NewSource(1))
	n := 0
	for i := 0; i < 200000; i++ {
		p := ""
		for j, k := 0, 1+r.Intn(7); j < k; j++ {
			p += atoms[r.Intn(len(atoms))]
		}
		src := ""
		for j, k := 0, r.Intn(12); j < k; j++ {
			src += subj[r.Intn(len(subj))]
		}
		prog, err := func() (prog *program, err error) {
			defer func() {
				if v := recover(); v != nil {
					err = fmt.Errorf("%v", v)
				}
			}()
			return compileProgram(p), nil
		}()
		if err == nil && (prog.lit != nil || prog.set != nil || prog.re != nil) {
			n++
		}
		offset := 0
		if r.Intn(4) == 0 {
			offset = r.Intn(len(src) + 1)
		}
		checkSame(t, p, []byte(src), offset, []int{-1, 1, 2}[r.Intn(3)])
	}
	t.Logf("%d patterns ran on a fast path", n)
}

var benchSrc = []byte(`<p>See <a href="https:&#x2F;&#x2F;news.ycombinator.com&#x2F;item?id=4242">this</a> and <a href="https:&#x2F;&#x2F;example.com">that</a>, which is a long comment w/ a bunch of text in it, like most of them are.</p>`)

func benchFind(b *testing.B, p string, vm bool) {
	prog := compileProgram(p)
	for i := 0; i < b.N; i++ {
		if vm {
			prog.findVM(benchSrc, 0, -1)
		} else if _, err := Find(p, benchSrc, 0, -1); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLiteralFast(b *testing.B) { benchFind(b, "news%.ycombinator%.com&#x2F;item%?id=", false) }
func BenchmarkLiteralVM(b *testing.B)   { benchFind(b, "news%.ycombinator%.com&#x2F;item%?id=", true) }
func BenchmarkRegexpFast(b *testing.B)  { benchFind(b, `href="([^"]*)"`, false) }
func BenchmarkRegexpVM(b *testing.B)    { benchFind(b, `href="([^"]*)"`, true) }
func BenchmarkSetFast(b *testing.B)     { benchFind(b, `[&<>"']`, false) }
func BenchmarkSetRegexp(b *testing.B) {
	prog := compileProgram(`[&<>"']`)
	prog.set, prog.re = nil, regexp.MustCompile(`(?s)[&<>"']`) // (what it ran on before)
	for i := 0; i < b.N; i++ {
		prog.findRegexp(benchSrc, 0, -1)
	}
}
func BenchmarkWordsFast(b *testing.B) { benchFind(b, `%a+`, false) }
func BenchmarkWordsVM(b *testing.B)   { benchFind(b, `%a+`, true) }
