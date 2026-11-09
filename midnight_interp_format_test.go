package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

// `{x:%spec}` formats an interpolation w/ string.format.
var midnightInterpFormatCases = []struct {
	source string
	want   string
}{
	{`local p = 0.4567; return "progress {p * 100:%.1f}%"`, "progress 45.7%"},
	{`local n = 255; return "{n:%x} {n:%X} {n:%05d} {n:%+d} {n:%-5d}|"`, "ff FF 00255 +255 255  |"},
	{`return "[{'s':%5s}] {65:%c} {1.5:%e} {1.5:%g} {'a':%q}"`, `[    s] A 1.500000e+00 1.5 "a"`},
	{`return [[block {3.14159:%.3f}]]`, "block 3.142"},
	// mixed w/ plain interpolation, method calls, nested strings
	{`local t = { v = 2, get = self -> self.v }; return "{t:get()} {t:get():%.1f} {t.v}"`, "2 2.0 2"},
	{`return "{"{1}":%q}"`, `"1"`},
	{`return "{ ({ 1, 2 })[2]:%d}"`, "2"},
	// not a format: escaped braces, single quotes
	{`return "\{1:%d\}"`, "{1:%d}"},
	{`return '{1:%d}'`, "{1:%d}"},
}

func TestMidnightInterpFormat(t *testing.T) {
	for _, c := range midnightInterpFormatCases {
		L := NewState()
		if err := L.DoMidnightString(c.source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n  got %q, want %q", c.source, got, c.want)
		}
		L.Close()
	}
}

func TestLuagenMidnightInterpFormat(t *testing.T) {
	for _, c := range midnightInterpFormatCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n%s\n  got %q, want %q", c.source, generated, got, c.want)
		}
		L.Close()
	}
}

func TestMidnightInterpFormatErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{`return "{1:%.2f %d}"`, "invalid interpolation format '%.2f %d'"},
		{`return "{1:%u}"`, "invalid interpolation format '%u'"},
		{`return "{1:%}"`, "invalid interpolation format '%'"},
		{"return \"{1:%d\n}\"", "unterminated interpolation format"},
	}
	for _, c := range cases {
		L := NewState()
		_, err := L.LoadMidnightString(c.source)
		L.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got %v, want error containing %q", c.source, err, c.want)
		}
	}
}
