package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

const jsonPrelude = "local { render, json, jsonText, array, object, Data } = require('midnight.markup')\n"

// JSON <script>s: the expression is encoded when rendered (see markup.lua).
var midnightJSONCases = []struct {
	source string
	want   string
}{
	// the two ways to write it
	{`local d = { a = 1 }; return render(<script type="application/json" id="d">{d}</script>)`,
		`<script id="d" type="application/json">{"a":1}</script>`},
	{`local d = { a = 1 }; return render(<Data id="d" value={d}/>)`,
		`<script id="d" type="application/json">{"a":1}</script>`},
	{"return render(<script type='application/json'>\n  { { 1, 2 } }\n</script>)",
		`<script type="application/json">[1,2]</script>`},
	// values
	{`return render(<Data value={{ s = 'hi', n = 3, f = 2.5, t = true, list = { 'a', 'b' }, nested = { x = { y = 1 } } }}/>)`,
		`<script type="application/json">{"f":2.5,"list":["a","b"],"n":3,"nested":{"x":{"y":1}},"s":"hi","t":true}</script>`},
	{`return render(<Data value={'just a string'}/>)`, `<script type="application/json">"just a string"</script>`},
	{`return render(<Data value={nil}/>)`, `<script type="application/json">null</script>`},
	{`return render(<Data value={0.1}/>)`, `<script type="application/json">0.1</script>`},
	{`return render(<Data value={-7}/>)`, `<script type="application/json">-7</script>`},
	// empty tables say which they are
	{`return render(<Data value={{ list = array(), map = object({}) }}/>)`,
		`<script type="application/json">{"list":[],"map":{}}</script>`},
	// nothing in the data can close the <script> (or start a comment)
	{`return render(<Data value={'</script><script>alert(1)</script> <!-- & '}/>)`,
		`<script type="application/json">"\u003c/script\u003e\u003cscript\u003ealert(1)\u003c/script\u003e \u003c!-- \u0026 "</script>`},
	{"return render(<Data value={'q\"b\\\\n\\n\\t\\1 \\226\\128\\168'}/>)",
		`<script type="application/json">"q\"b\\n\n\t\u0001 \u2028"</script>`},
	// nothing to encode is null (so JSON.parse gets JSON)
	{`return render(<script type="application/json">{nil}</script>)`, `<script type="application/json">null</script>`},
	{`return render(<script type="application/json">{not_defined}</script>)`, `<script type="application/json">null</script>`},
	// already encoded values pass through
	{`return render(<Data value={json({ 1 })}/>)`, `<script type="application/json">[1]</script>`},
	{`return render(<script type="application/json">{jsonText('{"from":"go"}')}</script>)`,
		`<script type="application/json">{"from":"go"}</script>`},
	// other scripts are still raw text
	{`return render(<script>if (a < b) { go() }</script>)`, `<script>if (a < b) { go() }</script>`},
	{`return render(<script type="application/ld+json">{"@type": "Thing"}</script>)`,
		`<script type="application/ld+json">{"@type": "Thing"}</script>`},
	// json() outside of a <script> is its text (it has nothing to escape)
	{`return render(<pre>{json({ a = '<b>' })}</pre>)`, `<pre>{"a":"\u003cb\u003e"}</pre>`},
}

func TestMidnightJSON(t *testing.T) {
	for _, c := range midnightJSONCases {
		source := jsonPrelude + c.source
		chunk, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
		if err != nil {
			t.Errorf("%s\n%v", c.source, err)
			continue
		}
		generated := luagen.Generate(chunk)
		for _, run := range []struct {
			name string
			fn   func(L *LState) error
		}{
			{"native", func(L *LState) error { return L.DoMidnightString(source) }},
			{"luagen", func(L *LState) error { return L.DoString(generated) }},
		} {
			L := NewState()
			if err := run.fn(L); err != nil {
				t.Errorf("%s (%s)\n%v", c.source, run.name, err)
			} else if got := L.Get(-1).String(); got != c.want {
				t.Errorf("%s (%s)\n  got  %s\n  want %s", c.source, run.name, got, c.want)
			}
			L.Close()
		}
	}
}

func TestMidnightJSONErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`return render(<Data value={{}}/>)`, `empty table could be [] or {}: use array(t) or object(t)`},
		{`return render(<Data value={{ a = { b = {} } }}/>)`, `empty table.a.b could be`},
		{`return render(<Data value={{ 1, nil, 3 }}/>)`, `has keys that aren't 1..n or all strings`},
		{`return render(<Data value={{ 1, x = 2 }}/>)`, `has keys that aren't 1..n or all strings`},
		{`return render(<Data value={array({ x = 1 })}/>)`, `array isn't a sequence`},
		{`local t = {}; t.self = t; return render(<Data value={t}/>)`, `json: cycle.self`},
		{`return render(<Data value={0/0}/>)`, `isn't a JSON number`},
		{`return render(<Data value={{ f = () -> 1 }}/>)`, `can't encode a function.f`},
		{`return render(<Data value={<b/>}/>)`, `can't encode an element`},
	} {
		L := NewState()
		err := L.DoMidnightString(jsonPrelude + c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got  %v\n  want %q", c.source, err, c.want)
		}
		L.Close()
	}
}

// A JSON <script> holds one expression, not text.
func TestMidnightJSONSyntaxErrors(t *testing.T) {
	for _, source := range []string{
		`return <script type="application/json">{"a": 1}</script>`,
		`return <script type="application/json">[1, 2]</script>`,
		`return <script type="application/json">{a} {b}</script>`,
	} {
		_, err := parse.ParseMidnight(strings.NewReader(source), "<string>")
		if err == nil || !strings.Contains(err.Error(), `holds one {expression}`) {
			t.Errorf("%s\n  got %v", source, err)
		}
	}
}

func TestMidnightJSONFormat(t *testing.T) {
	src := "return <script type=\"application/json\" id=\"d\">{data}</script>\n"
	out, err := format.Format([]byte(src), format.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != src {
		t.Errorf("fmt changed it:\n%s", out)
	}
}

// <script define:vars={{ name = value }}>: each name is declared in the script.
var midnightDefineVarsCases = []struct {
	source string
	want   string
}{
	{"return render(<script define:vars={{ repos = { 'a' }, n = 3 }}>go(repos, n)</script>)",
		"<script>const n = JSON.parse('3');\nconst repos = JSON.parse('[\"a\"]');\ngo(repos, n)</script>"},
	{"return render(<script type=\"module\" define:vars={{ x = jsonText('{\"from\":\"go\"}') }}>use(x)</script>)",
		"<script type=\"module\">const x = JSON.parse('{\"from\":\"go\"}');\nuse(x)</script>"},
	// quotes and backslashes are escaped for the js string, `<` for the html
	{"return render(<script define:vars={{ s = \"it's </script> \\\\\" }}>s</script>)",
		"<script>const s = JSON.parse('\"it\\'s \\\\u003c/script\\\\u003e \\\\\\\\\"');\ns</script>"},
	// other attributes named data are just attributes
	{"return render(<p data=\"x\">hi</p>)", "<p data=\"x\">hi</p>"},
	{"return render(<script data=\"x\">go()</script>)", "<script data=\"x\">go()</script>"},
}

func TestMidnightDefineVars(t *testing.T) {
	for _, c := range midnightDefineVarsCases {
		L := NewState()
		if err := L.DoMidnightString(jsonPrelude + c.source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.source, got, c.want)
		}
		L.Close()
	}
}

func TestMidnightDefineVarsErrors(t *testing.T) {
	for _, c := range []struct{ source, want string }{
		{`return render(<script define:vars={{ ['my-repos'] = 1 }}/>)`, `my-repos isn't a js variable name`},
		{`return render(<script define:vars={{ class = 1 }}/>)`, `class isn't a js variable name`},
		{`return render(<script define:vars={{ 1 }}/>)`, `1 isn't a js variable name`},
		{`return render(<script define:vars={'x'}/>)`, `takes a table of names to values`},
		{`return render(<p define:vars={{ a = 1 }}>hi</p>)`, `define:vars only works on a <script> (it's on a <p>)`},
		{`return render(<script define:vars={{ repos = {} }}/>)`, `<script define:vars={...}>: repos: empty table could be [] or {}`},
	} {
		L := NewState()
		err := L.DoMidnightString(jsonPrelude + c.source)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got  %v\n  want %q", c.source, err, c.want)
		}
		L.Close()
	}
}

// An undefined component is an error, not an empty fragment.
func TestMidnightMarkupNilComponent(t *testing.T) {
	for _, source := range []string{
		`return render(<Nope/>)`,
		`return render(<Nope id="x">hi</Nope>)`,
		`local ui = {}; return render(<ui.Missing/>)`,
		`return render(<div><Nope/></div>)`,
	} {
		chunk, err := parse.ParseMidnight(strings.NewReader(jsonPrelude+source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", source, err)
		}
		generated := luagen.Generate(chunk)
		for name, run := range map[string]func(L *LState) error{
			"native": func(L *LState) error { return L.DoMidnightString(jsonPrelude + source) },
			"luagen": func(L *LState) error { return L.DoString(generated) },
		} {
			L := NewState()
			err := run(L)
			if err == nil || !strings.Contains(err.Error(), "component is nil (not defined, or not imported?)") {
				t.Errorf("%s (%s): got %v", source, name, err)
			}
			L.Close()
		}
	}
	// fragments still work
	L := NewState()
	defer L.Close()
	if err := L.DoMidnightString(jsonPrelude + `return render(<>a<b/></>)`); err != nil {
		t.Fatal(err)
	} else if got := L.Get(-1).String(); got != "a<b></b>" {
		t.Errorf("fragment: %s", got)
	}
}
