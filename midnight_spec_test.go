package lua

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// Differential tests against the Erde reference compiler, using the snippets
// from its spec suite (see _tools/midnight-spec/update.sh).
//
// Each valid snippet runs three ways, which must all give the same results:
//   - the reference compiler's Lua output (NNNN.ref.lua)
//   - the snippet, parsed natively (parse.ParseMidnight)
//   - the snippet, compiled to Lua (luagen)

const midnightSpecDir = "testdata/midnight-spec"

func TestMidnightSpecValid(t *testing.T) {
	sources, _ := filepath.Glob(filepath.Join(midnightSpecDir, "valid", "*.mn"))
	if len(sources) == 0 {
		t.Fatal("no spec snippets found")
	}

	for _, path := range sources {
		source, _ := os.ReadFile(path)
		ref, _ := os.ReadFile(strings.TrimSuffix(path, ".mn") + ".ref.lua")

		want := midnightSpecRun(func(L *LState) error { return L.DoString(string(ref)) })
		native := midnightSpecRun(func(L *LState) error { return L.DoMidnightString(string(source)) })

		var generated string
		compiled := midnightSpecRun(func(L *LState) error {
			chunk, err := parse.ParseMidnight(strings.NewReader(string(source)), "<string>")
			if err != nil {
				return err
			}
			generated = luagen.Generate(chunk)
			return L.DoString(generated)
		})

		if native != want {
			t.Errorf("%s (native)\n%s\n  got:  %s\n  want: %s", path, source, native, want)
		}
		if compiled != want {
			t.Errorf("%s (luagen)\n%s\n%s\n  got:  %s\n  want: %s", path, source, generated, compiled, want)
		}
	}
}

func TestMidnightSpecInvalid(t *testing.T) {
	sources, _ := filepath.Glob(filepath.Join(midnightSpecDir, "invalid", "*.mn"))
	if len(sources) == 0 {
		t.Fatal("no spec snippets found")
	}

	for _, path := range sources {
		source, _ := os.ReadFile(path)
		reason, _ := os.ReadFile(strings.TrimSuffix(path, ".mn") + ".txt")

		L := NewState()
		if _, err := L.LoadMidnightString(string(source)); err == nil {
			t.Errorf("%s: accepted invalid snippet (reference: %s)\n%s", path, reason, source)
		}
		L.Close()
	}
}

var midnightSpecPositions = regexp.MustCompile(`<string>:\d+:|line:\d+\(column:\d+\)|at EOF:`)

// midnightSpecRun runs a snippet and returns its results (or error) as a string.
func midnightSpecRun(run func(*LState) error) string {
	L := NewState()
	defer L.Close()
	L.SetGlobal("print", L.NewFunction(func(*LState) int { return 0 }))

	if err := run(L); err != nil {
		msg := strings.SplitN(err.Error(), "\n", 2)[0]
		return "error: " + midnightSpecPositions.ReplaceAllString(msg, "<pos>")
	}

	results := []string{}
	for i := 1; i <= L.GetTop(); i++ {
		results = append(results, midnightSpecSerialize(L.Get(i), map[*LTable]bool{}))
	}
	return strings.Join(results, ", ")
}

func midnightSpecSerialize(value LValue, seen map[*LTable]bool) string {
	switch v := value.(type) {
	case *LTable:
		if seen[v] {
			return "<cycle>"
		}
		seen[v] = true
		fields := []string{}
		v.ForEach(func(key, value LValue) {
			fields = append(fields, midnightSpecSerialize(key, seen)+"="+midnightSpecSerialize(value, seen))
		})
		sort.Strings(fields)
		return "{" + strings.Join(fields, ",") + "}"
	case *LFunction:
		return "<function>"
	case LString:
		return fmt.Sprintf("%q", string(v))
	}
	return value.String()
}
