package lua

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
)

// toGo converts a value back from lua for comparing: tables w/ only 1..n keys
// are lists, other tables maps (w/ string keys, numbers as strings).
func toGo(v LValue) any {
	switch lv := v.(type) {
	case *LNilType:
		return nil
	case LBool:
		return bool(lv)
	case LNumber:
		return float64(lv)
	case LString:
		return string(lv)
	case *LTable:
		if n := lv.Len(); n > 0 {
			count := 0
			lv.ForEach(func(LValue, LValue) { count++ })
			if count == n {
				list := make([]any, n)
				for i := 1; i <= n; i++ {
					list[i-1] = toGo(lv.RawGetInt(i))
				}
				return list
			}
		}
		m := map[string]any{}
		lv.ForEach(func(k, val LValue) { m[k.String()] = toGo(val) })
		return m
	}
	return v.String()
}

// literal values, and what they are back from lua (nil when it's the same)
var literalCases = []struct {
	in   any
	want any
}{
	{nil, nil},
	{true, nil},
	{"plain", nil},
	{"it's a \\ \"quote\"\n\ttab {not interpolated} 🌙", nil},
	{"\x00\x01\x7f9", nil}, // (an escaped byte followed by a digit)
	{42, float64(42)},
	{-3.5, nil},
	{1e300, nil},
	{[]any{1, "two", true}, []any{float64(1), "two", true}},
	{[]string{}, map[string]any{}},
	{map[string]any{"title": "x", "end": 1, "match": 2, "has space": 3, "nil value": nil}, map[string]any{"title": "x", "end": float64(1), "match": float64(2), "has space": float64(3)}},
	{map[string]any{"pages": []any{map[string]any{"path": "/", "file": "index.md"}}}, map[string]any{"pages": []any{map[string]any{"path": "/", "file": "index.md"}}}},
	{map[int]string{2: "b", 10: "j"}, map[string]any{"2": "b", "10": "j"}},
}

func TestLiteral(t *testing.T) {
	for _, c := range literalCases {
		src, err := luagen.Literal(c.in)
		if err != nil {
			t.Errorf("%#v: %v", c.in, err)
			continue
		}
		want := c.want
		if want == nil {
			want = c.in
		}
		for _, run := range []struct {
			name string
			fn   func(L *LState, code string) error
		}{
			{"midnight", func(L *LState, code string) error { return L.DoMidnightString(code) }},
			{"lua", func(L *LState, code string) error { return L.DoString(code) }},
		} {
			L := NewState()
			if err := run.fn(L, "return "+src); err != nil {
				t.Errorf("%s (%s): %v", src, run.name, err)
			} else if got := toGo(L.Get(-1)); !reflect.DeepEqual(got, want) {
				t.Errorf("%s (%s)\n  got  %#v\n  want %#v", src, run.name, got, want)
			}
			L.Close()
		}
	}
}

func TestLiteralForm(t *testing.T) {
	// sorted keys, names when they're names in both languages
	got, err := luagen.Literal(map[string]any{"b": 1, "a": 2, "end": 3, "match": 4, "1x": 5})
	if want := `{ ['1x'] = 5, a = 2, b = 1, ['end'] = 3, match = 4 }`; err != nil || got != want {
		t.Errorf("got %q (%v), want %q", got, err, want)
	}
	for v, want := range map[float64]string{3: "3", -0.25: "-0.25", math.Inf(1): "math.huge", 1e20: "1e+20"} {
		if got, _ := luagen.Literal(v); got != want {
			t.Errorf("Literal(%v) = %q, want %q", v, got, want)
		}
	}
	if got, _ := luagen.Literal(math.NaN()); got != "(0/0)" {
		t.Errorf("Literal(NaN) = %q", got)
	}
	if _, err := luagen.Literal(func() {}); err == nil || !strings.Contains(err.Error(), "func") {
		t.Errorf("a func: %v", err)
	}
	cycle := map[string]any{}
	cycle["self"] = cycle
	if _, err := luagen.Literal(cycle); err == nil {
		t.Error("a cycle didn't fail")
	}
}
