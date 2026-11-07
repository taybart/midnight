package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

var midnightMarkupOptions = parse.MidnightOptions{Markup: true}

const midnightMarkupPrelude = "local { h, render, map, raw, document } = require('midnight.markup')\n"

var midnightMarkupCases = []struct {
	source string
	want   string
}{
	// elements, attributes, text
	{`return render(<div/>)`, `<div></div>`},
	{`return render(<br/>)`, `<br>`},
	{`return render(<p>hi</p>)`, `<p>hi</p>`},
	{`return render(<a href="/x" id='y'>link</a>)`, `<a href="/x" id="y">link</a>`},
	{`local n = 3; return render(<a data-n={n} hx-post="/p" x-on:click="go()" @click="go()">x</a>)`, `<a @click="go()" data-n="3" hx-post="/p" x-on:click="go()">x</a>`},
	{`local kind = 'big'; return render(<b class="btn btn-{kind}"/>)`, `<b class="btn btn-big"></b>`},
	{`return render(<input disabled checked={false} value={nil}/>)`, `<input disabled>`},
	{`return render(<b style={{ color = 'red', ['font-size'] = '2em' }}/>)`, `<b style="color: red; font-size: 2em"></b>`},
	{`local on = true; return render(<b class={{ 'a', 'b', on = on, off = false }}/>)`, `<b class="a b on"></b>`},
	{`return render(<b onclick={() -> 1}/>)`, `<b></b>`},
	// text whitespace (same as JSX)
	{"return render(<p>\n  hello\n  world\n</p>)", `<p>hello world</p>`},
	{"return render(<p>\n  <b>a</b>\n  <b>b</b>\n</p>)", `<p><b>a</b><b>b</b></p>`},
	{"return render(<p>  a  {1}  b  </p>)", `<p>  a  1  b  </p>`},
	{"return render(<p>a&nbsp;&amp;&lt;&#65;&#x42;&bogus; & b</p>)", "<p>a &amp;&lt;AB&amp;bogus; &amp; b</p>"},
	// expressions
	{`local name = 'x'; return render(<p>hi {name}!</p>)`, `<p>hi x!</p>`},
	{`return render(<p>{1 + 2}{'a' .. 'b'}{2 > 1 && 'y'}</p>)`, `<p>3aby</p>`},
	{`return render(<p>{nil}{false}{true}{}{--[[ comment ]]}</p>)`, `<p></p>`},
	{`local f = () -> (1, 2); return render(<p>{f()}</p>)`, `<p>1</p>`},
	{`local t = { x = 'a' }; return render(<p>{t.x}{t['x']}{({ 'b' })[1]}</p>)`, `<p>aab</p>`},
	{`return render(<ul>{map({ 'a', 'b' }, (x, i) -> <li>{i}{x}</li>)}</ul>)`, `<ul><li>1a</li><li>2b</li></ul>`},
	{`return render(<p>{<b>{<i>deep</i>}</b>}</p>)`, `<p><b><i>deep</i></b></p>`},
	{`local ok = false; return render(<p>{ok && <b/> || <i/>}</p>)`, `<p><i></i></p>`},
	{`return render(<p>{"str {1 + 1}"}</p>)`, `<p>str 2</p>`},
	// escaping
	{`local s = '<script>"&\'</script>'; return render(<p title={s}>{s}</p>)`, `<p title="&lt;script&gt;&quot;&amp;&#39;&lt;/script&gt;">&lt;script&gt;&quot;&amp;&#39;&lt;/script&gt;</p>`},
	{`return render(<p>{raw('<b>trusted</b>')}</p>)`, `<p><b>trusted</b></p>`},
	{`return render(<script>{'if (a < b) {}'}</script>)`, `<script>{'if (a < b) {}'}</script>`}, // literal, not an expression
	// script / style content is raw text (like HTML)
	{"return render(<style>\n  html, body { color: #fff; }\n  a > b { x: y }\n</style>)", "<style>\n  html, body { color: #fff; }\n  a > b { x: y }\n</style>"},
	{"return render(<script type=\"module\">\n  // it's js\n  if (a < b && c) { go(`${x}`) }\n</script>)", "<script type=\"module\">\n  // it's js\n  if (a < b && c) { go(`${x}`) }\n</script>"},
	{"return render(<p><style>{</style>{1}</p>)", "<p><style>{</style>1</p>"},
	{"return render(<style>  </style>)", "<style></style>"},
	// fragments
	{`return render(<>a<b/>c</>)`, `a<b></b>c`},
	{`return render(<p><>{1}{2}</></p>)`, `<p>12</p>`},
	// components
	{`local Hi = props -> <p>hi {props.name}</p>; return render(<Hi name="x"/>)`, `<p>hi x</p>`},
	{`local Box = props -> <div class="box">{props.children}</div>; return render(<Box><b/>text</Box>)`, `<div class="box"><b></b>text</div>`},
	{`local ui = { Btn = p -> <button>{p.label}</button> }; return render(<ui.Btn label="ok"/>)`, `<button>ok</button>`},
	{`local Nil = () -> nil; return render(<p><Nil/></p>)`, `<p></p>`},
	// spreads (merged left to right)
	{`local p = { a = 1, b = 2 }; return render(<i b="x" {...p} c={3}/>)`, `<i a="1" b="2" c="3"></i>`},
	{`local p = { a = 1 }; return render(<i {...p} a="z"/>)`, `<i a="z"></i>`},
	{`local p; return render(<i {...p} {...nil} a="z"/>)`, `<i a="z"></i>`},
	{`local p = { name = 'x' }; local Hi = props -> <p>{props.name}</p>; return render(<Hi {...p}/>)`, `<p>x</p>`},
	// positions where `<` is still an operator
	{`local a, b = 1, 2; return a < b`, `true`},
	{`local a, b = 1, 2; return a<b`, `true`},
	{`local t = { 1 }; return t[1]<2`, `true`},
	{`local f = () -> 1; return f()<2`, `true`},
	{`return 1<2`, `true`},
	{`return 'a'<'b'`, `true`},
	{`local x = 1; return (x)<2 && x <= 1 && 1 << 1 == 2`, `true`},
	// where elements may start
	{`local f = () -> <p/>; return render(f())`, `<p></p>`},
	{`local function f() { return <p/> } return render(f())`, `<p></p>`},
	{`local t = { <a/>, k = <b/> }; return render(t[1]) .. render(t.k)`, `<a></a><b></b>`},
	{`local s = render(<a/>) .. render(<b/>); return s`, `<a></a><b></b>`},
	{`return render((<p/>))`, `<p></p>`},
	{`local x = 1; local e = x > 0 && <p/> || <i/>; return render(e)`, `<p></p>`},
	// document, tostring
	{`return document(<html/>)`, `<!DOCTYPE html><html></html>`},
	{`return tostring(<p>x</p>)`, `<p>x</p>`},
	// multiline, nested
	{`local page = <html>
  <head>
    <title>t</title>
  </head>
  <body
    class="x" -- a comment in a tag
    id="y"
  >
    text that ends w/ a quote's apostrophe
  </body>
</html>
return render(page)`, `<html><head><title>t</title></head><body class="x" id="y">text that ends w/ a quote&#39;s apostrophe</body></html>`},
}

func TestMidnightMarkup(t *testing.T) {
	for _, c := range midnightMarkupCases {
		source := midnightMarkupPrelude + c.source
		L := NewState(Options{Midnight: midnightMarkupOptions})
		if err := L.DoMidnightString(source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n  got  %s\n  want %s", c.source, got, c.want)
		}
		L.Close()
	}
}

func TestLuagenMidnightMarkup(t *testing.T) {
	for _, c := range midnightMarkupCases {
		source := midnightMarkupPrelude + c.source
		chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(source), "<string>", midnightMarkupOptions)
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
		} else if got := L.Get(-1).String(); got != c.want {
			t.Errorf("%s\n%s\n  got  %s\n  want %s", c.source, generated, got, c.want)
		}
		L.Close()
	}
}

// Without an `h` in scope, the runtime's `h` is imported automatically.
var midnightMarkupAutoCases = []struct {
	source string
	want   string
}{
	{`return tostring(<p class="x">hi</p>)`, `<p class="x">hi</p>`},
	{`local Card = props -> <div>{props.children}</div>
return require('midnight.markup').render(<Card><b/></Card>)`, `<div><b></b></div>`},
	// a declared `h` (local, or a parameter) wins
	{`local h = (tag) -> 'mine ' .. tag; return <p/>`, `mine p`},
	{`local f = h -> <p/>; return f(t -> 'param ' .. t)`, `param p`},
	// the import doesn't clash w/ user names
	{`local require = 1; local midnight = 2; return tostring(<p/>)`, `<p></p>`},
	// works w/ `module` declarations
	{`module x = tostring(<p/>)`, ``},
}

func TestMidnightMarkupAutomaticRuntime(t *testing.T) {
	for _, c := range midnightMarkupAutoCases {
		chunk, err := parse.ParseMidnightWithOptions(strings.NewReader(c.source), "<string>", midnightMarkupOptions)
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		for _, run := range []struct {
			name string
			fn   func(L *LState) error
		}{
			{"native", func(L *LState) error { return L.DoMidnightString(c.source) }},
			{"luagen", func(L *LState) error { return L.DoString(generated) }},
		} {
			L := NewState(Options{Midnight: midnightMarkupOptions})
			if err := run.fn(L); err != nil {
				t.Errorf("%s (%s)\n%s\n%v", c.source, run.name, generated, err)
			} else if got := L.Get(-1); c.want != "" && got.String() != c.want {
				t.Errorf("%s (%s)\n%s\n  got  %s\n  want %s", c.source, run.name, generated, got, c.want)
			}
			L.Close()
		}
	}
}

// Elements are lowered to calls of the factory, which can be renamed.
func TestMidnightMarkupFactory(t *testing.T) {
	source := `local react = { createElement = (tag, props, ...) -> ({ tag = tag, n = select('#', ...) }) }
local e = <div a="1">x{1}<b/></div>
return e.tag .. e.n`
	L := NewState(Options{Midnight: parse.MidnightOptions{Markup: true, MarkupFactory: "react.createElement"}})
	defer L.Close()
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "div3" {
		t.Errorf("got %s, want div3", got)
	}
}

func TestMidnightMarkupSyntaxErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{`local x = <p>`, "unterminated markup element"},
		{`local x = <p>hi</b>`, "expected closing tag '</p>' (for line 2) got '</b>'"},
		{`local x = <p></>`, "expected closing tag '</p>'"},
		{`local x = <>a</p>`, "expected closing tag '</>'"},
		{`local x = <p a=1/>`, "expected attribute value"},
		{`local x = <p a={}/>`, "can't be empty"},
		{`local x = <p {a}/>`, "expected '...'"},
		{`local x = <p !/>`, "expected attribute, '>', or '/>'"},
		{`local x = <p>{1</p>`, "unterminated markup expression"},
		{`local x = <p>{-- line comments eat the brace}</p>`, "unterminated markup expression"},
		{`local x = <br>child</br>; require('midnight.markup').render(x)`, "can't have children"},
	}

	for _, c := range cases {
		L := NewState(Options{Midnight: midnightMarkupOptions})
		err := L.DoMidnightString(midnightMarkupPrelude + c.source)
		L.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got %v, want error containing %q", c.source, err, c.want)
		}
	}
}

// Without the experiment, `<` is always an operator.
func TestMidnightMarkupDisabled(t *testing.T) {
	L := NewState()
	defer L.Close()
	if _, err := L.LoadMidnightString(`local x = <p/>`); err == nil {
		t.Error("markup parsed w/o the markup experiment")
	}
}

// jsx is the old (deprecated) name of the experiment and the runtime.
func TestMidnightMarkupOldName(t *testing.T) {
	var opts parse.MidnightOptions
	if err := opts.Enable("match,jsx=ui.h"); err != nil || !opts.Markup || opts.MarkupFactory != "ui.h" {
		t.Fatalf("-X jsx: %+v %v", opts, err)
	}
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	if err := L.DoMidnightString(`assert(require('midnight.jsx') == require('midnight.markup'), 'midnight.jsx')`); err != nil {
		t.Fatal(err)
	}
}

func TestFormatMidnightMarkup(t *testing.T) {
	in := "local x=<div   class='a'>\n  hi {name}\n    <b/>\n</div>\nlocal y=a<b"
	want := "local x = <div   class='a'>\n  hi {name}\n    <b/>\n</div>\nlocal y = a < b\n"
	got, err := format.Format([]byte(in), format.Options{Midnight: midnightMarkupOptions})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// midnightMarkupActionsPrelude finds action ids in rendered html (in order).
const midnightMarkupActionsPrelude = `local markup = require('midnight.markup')
local { state, render } = markup
local ids = (html) -> {
  local out = {}
  for id in html:gmatch("midnight%.rpc%('([^']+)'") { table.insert(out, id) }
  return out
}
`

// mount registers `on*` functions as actions, and dispatch runs them.
func TestMidnightMarkupActions(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()

	source := midnightMarkupActionsPrelude + `local count, last = 0, nil
local App = () -> <main>
  <button onclick={() -> { count += 1 }} on:dblclick={e -> { last = e.type }}>{count}</button>
  <p onClick={'not a function'}>{last}</p>
</main>
local root = () -> <App/>
local page = markup.document(markup.mount(root, { rpc = '/rpc', headers = { ['X-B'] = 's"1', ['X-A'] = 'a' } }))
local [dbl, click] = ids(page)
markup.dispatch(click, {})
markup.dispatch(click)
markup.dispatch(dbl, { type = 'dblclick' })
local res = markup.dispatch(click)
-- a stale action gets the whole page (the client is out of date)
local stale = markup.dispatch('nope').body
return page, res.body, res.headers['Content-Type'], dbl .. ' ' .. click, stale`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}

	page, body, contentType, ids, stale := L.Get(-5).String(), L.Get(-4).String(), L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String()
	// attributes render sorted, so `on:dblclick` is #1 and `onclick` is #2
	if ids != "r:1#1 r:1#2" {
		t.Errorf("action ids: %q", ids)
	}
	wantButton := `<button ondblclick="midnight.rpc('r:1#1', event)" onclick="midnight.rpc('r:1#2', event)">0</button>`
	for _, want := range []string{wantButton, `<main data-midnight="r:1">`, `<p onClick="not a function"></p>`, "<script>", `headers: { 'X-A': 'a', 'X-B': 's\u00221' }`, "</main><script"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q\n%s", want, page)
		}
	}
	// App is the only component, so it's what changed
	if want := `<template data-midnight-target="r:1"><main data-midnight="r:1">`; !strings.HasPrefix(body, want) || !strings.Contains(body, ">3</button><p onClick=\"not a function\">dblclick</p></main></template>") {
		t.Errorf("dispatch body\n%s", body)
	}
	if contentType != "text/html; charset=utf-8" {
		t.Errorf("content type %q", contentType)
	}
	if !strings.HasPrefix(stale, "<!DOCTYPE html>") || !strings.Contains(stale, ">3</button>") {
		t.Errorf("stale action: expected the whole (current) page, got\n%s", stale)
	}
}

// dispatch only sends the components whose own markup changed.
func TestMidnightMarkupFragments(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()

	source := midnightMarkupActionsPrelude + `local title, note, outside = 'hi', 'n', 'o'
local Counter = (props) -> {
  local s = state({ count = props.start })
  return <button onclick={() -> { s.count += 1 }}>{s.count}</button>
}
local Note = () -> <>{note}</> -- no root element of its own
local Card = () -> <section><Note/><button onclick={() -> { note = 'N' }}>n</button></section>
local App = () -> <main>
  <h1>{title}</h1>
  <Counter start={1}/>
  <Counter start={10}/>
  <Card/>
  <button onclick={() -> { title = 'HI' }}>t</button>
  <button onclick={() -> {}}>noop</button>
  <button onclick={() -> { outside = 'O' }}>o</button>
</main>
local root = () -> <div>{outside}<App/></div>
local page = markup.document(markup.mount(root, { rpc = '/rpc', morph = false }))
local [a, b, n, t, noop, o] = ids(page)
local out = {}
for _, id in ipairs({ b, noop, n, t, a, o }) {
  table.insert(out, markup.dispatch(id).body)
}
return table.concat(out, '\n')`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}

	bodies := strings.Split(L.Get(-1).String(), "\n")
	cases := []struct {
		name string
		want string
	}{
		{"one counter", `<template data-midnight-target="r.2:1.3:2"><button data-midnight="r.2:1.3:2" onclick="midnight.rpc('r.2:1.3:2#1', event)">11</button></template>`},
		{"nothing changed", ``},
		{"w/o a root element, its parent", `<template data-midnight-target="r.2:1.4:3"><section data-midnight="r.2:1.4:3">N<button onclick="midnight.rpc('r.2:1.4:3#1', event)">n</button></section></template>`},
		{"a parent covers its children", `<template data-midnight-target="r.2:1"><main data-midnight="r.2:1"><h1>HI</h1>`},
		{"the other counter", `<template data-midnight-target="r.2:1.2:2"><button data-midnight="r.2:1.2:2" onclick="midnight.rpc('r.2:1.2:2#1', event)">2</button></template>`},
		{"outside of components, the whole page", `<!DOCTYPE html><div>O<main`},
	}
	for i, c := range cases {
		if i >= len(bodies) {
			t.Fatalf("missing body for %s", c.name)
		}
		if !strings.HasPrefix(bodies[i], c.want) || (c.want == "" && bodies[i] != "") {
			t.Errorf("%s:\n  got  %s\n  want %s", c.name, bodies[i], c.want)
		}
		if i == 3 && strings.Count(bodies[i], "<template") != 1 {
			t.Errorf("%s: expected one template\n%s", c.name, bodies[i])
		}
	}
}

// A component w/ a document root (ex. <html>) can't be sent on its own.
func TestMidnightMarkupFragmentsDocument(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := midnightMarkupActionsPrelude + `local x = 1
local App = () -> <html><body><button onclick={() -> { x += 1 }}>{x}</button></body></html>
local page = markup.document(markup.mount(() -> <App/>, { rpc = '/rpc', morph = false }))
return markup.dispatch(ids(page)[1]).body`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); !strings.HasPrefix(got, "<!DOCTYPE html><html") || !strings.Contains(got, ">2</button>") {
		t.Errorf("got\n%s", got)
	}
}

// Without mount, functions in attributes are skipped (plain server rendering).
func TestMidnightMarkupActionsUnmounted(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	if err := L.DoMidnightString(`return require('midnight.markup').document(<b onclick={() -> 1}/>)`); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "<!DOCTYPE html><b></b>" {
		t.Errorf("got %s", got)
	}
}

// state is kept per component instance (by position in the tree, or `key`).
func TestMidnightMarkupState(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()

	source := midnightMarkupActionsPrelude + `local show, items, order = false, { 'x', 'y' }, {}

local Counter = (props) -> {
  local s = state({ count = props.start || 0 })
  local calls = state(() -> ({ n = 0 }))
  calls.n += 1
  table.insert(order, props.name)
  return <button onclick={() -> { s.count += 1 }}>{props.name}={s.count}</button>
}
local Badge = () -> <i>badge</i>
local Item = (props) -> {
  local s = state({ clicks = 0 })
  return <li onclick={() -> { s.clicks += 1 }}>{props.name}:{s.clicks}</li>
}
local App = () -> {
  table.insert(order, 'App')
  return <main>
    {show && <Badge/>}
    <Counter name="a"/>
    <Counter name="b" start={10}/>
    <ul>{markup.map(items, it -> <Item key={it} name={it}/>)}</ul>
  </main>
}

local root = () -> <App/>
local out = {}
markup.mount(root, { rpc = '/rpc', morph = false })
local page = render(root())
table.insert(out, page)
local [a, b, x, y] = ids(page)
markup.dispatch(b)                      -- b += 1
markup.dispatch(b)                      -- b += 1
show = true                          -- a conditional sibling doesn't move state
markup.dispatch(a)                      -- a += 1
markup.dispatch(y)                      -- y += 1
items = { 'y', 'x' }                 -- keys keep item state when reordering
table.insert(out, render(root()))
items = { 'x' }                      -- y is gone, so is its state
render(root())
items = { 'x', 'y' }
table.insert(out, render(root()))
return table.concat(out, '\n'), table.concat(order, ' ', 1, 3)`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}

	renders := strings.Split(L.Get(-2).String(), "\n")
	strip := func(s string) string { // just the text
		var b strings.Builder
		in := false
		for _, c := range s {
			switch {
			case c == '<':
				in = true
			case c == '>':
				in = false
				b.WriteByte(' ')
			case !in:
				b.WriteRune(c)
			}
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}
	want := []string{
		"a=0 b=10 x:0 y:0",
		"badge a=1 b=12 y:1 x:0",
		"badge a=1 b=12 x:0 y:0",
	}
	for i := range want {
		if got := strip(renders[i]); got != want[i] {
			t.Errorf("render %d: got %q, want %q\n%s", i, got, want[i], renders[i])
		}
	}
	// components run top down (parents first)
	if got := L.Get(-1).String(); got != "App a b" {
		t.Errorf("render order: got %q", got)
	}
	// component roots are marked (for targeted updates)
	if !strings.Contains(renders[0], `<main data-midnight="r:`) || !strings.Contains(renders[0], `<li data-midnight="r:1.4.1.kx:`) {
		t.Errorf("missing data-midnight\n%s", renders[0])
	}

	if err := L.DoMidnightString(`require('midnight.markup').state({})`); err == nil || !strings.Contains(err.Error(), "while rendering a component") {
		t.Errorf("got %v", err)
	}
}

// `-X markup=name` sets the factory.
func TestMidnightMarkupFactoryOption(t *testing.T) {
	var opts parse.MidnightOptions
	if err := opts.Enable("match, markup=harmony.ui.h"); err != nil {
		t.Fatal(err)
	}
	if !opts.Match || !opts.Markup || opts.MarkupFactory != "harmony.ui.h" {
		t.Errorf("got %+v", opts)
	}
	for _, bad := range []string{"markup=", "markup=a..b", "markup=1x", "markup=a-b", "markup=local", "match=1", "nope"} {
		if err := (&parse.MidnightOptions{}).Enable(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}

// W/o an rpc url, actions POST to the page's own url (as the browser sees it,
// ex. behind a proxy that strips a prefix).
func TestMidnightMarkupDefaultRPC(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := `local markup = require('midnight.markup')
local live = () -> <b onclick={() -> {}}/>
local own = markup.document(markup.mount(live, { morph = false }))
local set = markup.document(markup.mount(live, { rpc = '/actions', morph = false }))
local ok, err = pcall(markup.mount, live, { rpc = 42 })
return own, set, tostring(err)`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	own, set, err := L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String()
	if !strings.Contains(own, "{ rpc: null,") || !strings.Contains(own, "cfg.rpc ?? location.pathname + location.search") {
		t.Errorf("w/o rpc:\n%s", own)
	}
	if !strings.Contains(set, "{ rpc: '/actions',") {
		t.Errorf("w/ rpc:\n%s", set)
	}
	if !strings.Contains(err, "rpc must be a url") {
		t.Errorf("bad rpc: %s", err)
	}
}

// A mounted page w/o actions is static: no client script.
func TestMidnightMarkupStaticPage(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := `local markup = require('midnight.markup')
local static = markup.document(markup.mount(() -> <p>hi</p>, { rpc = '/' }))
local staticInteractive = markup.isInteractive()
local live = markup.document(markup.mount(() -> <b onclick={() -> {}}/>, { rpc = '/' }))
return static, tostring(staticInteractive), tostring(markup.isInteractive()), tostring(live:find('<script>', 1, true) != nil)`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-4).String(); got != "<!DOCTYPE html><p>hi</p>" {
		t.Errorf("static page: %s", got)
	}
	if a, b, c := L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String(); a != "false" || b != "true" || c != "true" {
		t.Errorf("interactive: %s %s, script: %s", a, b, c)
	}
}

// The runtime's bytecode is shared, but each state gets its own module.
func TestMidnightMarkupSharedRuntime(t *testing.T) {
	a, b := NewState(), NewState()
	defer a.Close()
	defer b.Close()
	if err := a.DoString(`require('midnight.markup').x = 1`); err != nil {
		t.Fatal(err)
	}
	if err := b.DoString(`assert(require('midnight.markup').x == nil, 'module shared between states')`); err != nil {
		t.Fatal(err)
	}
}

// idiomorph is inlined from the vendored copy by default; a url loads it
// from there instead, and false turns it off.
func TestMidnightMarkupMorph(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := `local markup = require('midnight.markup')
local page = morph -> markup.document(markup.mount(() -> <b onclick={() -> {}}/>, { rpc = '/', morph = morph }))
return page(nil), page('/static/idiomorph.js'), page(false)`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	vendored, url, off := L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String()
	if !strings.Contains(vendored, "<script>var Idiomorph=function(){") || strings.Contains(vendored, "unpkg") {
		t.Errorf("vendored: not inlined\n%.300s", vendored)
	}
	if !strings.Contains(url, `<script src="/static/idiomorph.js"></script>`) || strings.Contains(url, "var Idiomorph") {
		t.Errorf("url\n%.300s", url)
	}
	if strings.Contains(off, "Idiomorph=") || strings.Contains(off, "<script src") {
		t.Errorf("off\n%.300s", off)
	}
}

// css turns tables into scoped styles.
func TestMidnightMarkupCSS(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := `local markup = require('midnight.markup')
local s = markup.css({
  card = {
    padding = '1rem',
    backgroundColor = '#222',
    border_top_width = 2,
    ['&:hover'] = { borderColor = 'gold' },
    ['&.big, &:focus'] = { padding = '2rem' },
    ['$title'] = { margin = 0 },
    img = { width = '100%' },
    ['@media (max-width: 600px)'] = { padding = '.5rem', ['&:hover'] = { color = 'red' } },
  },
  title = { color = 'gold' },
})
local again = markup.css({ title = { color = 'gold' }, card = {
  padding = '1rem', backgroundColor = '#222', border_top_width = 2,
  ['&:hover'] = { borderColor = 'gold' }, ['&.big, &:focus'] = { padding = '2rem' },
  ['$title'] = { margin = 0 }, img = { width = '100%' },
  ['@media (max-width: 600px)'] = { padding = '.5rem', ['&:hover'] = { color = 'red' } },
} })
local other = markup.css({ title = { color = 'red' } })
return s.card, s.title, again.card, other.title, markup.styles(), markup.render(<p class={s.card}/>), markup.document(<html><head><title>t</title></head><body/></html>), markup.document(<p/>), markup.document(<html lang="en"><body/></html>)`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	card, title, again, other := L.Get(-9).String(), L.Get(-8).String(), L.Get(-7).String(), L.Get(-6).String()
	styles, rendered, doc, bare, noHead := L.Get(-5).String(), L.Get(-4).String(), L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String()
	if !strings.HasPrefix(noHead, `<!DOCTYPE html><html lang="en"><head><style data-midnight-css>`) || !strings.HasSuffix(noHead, "</style></head><body></body></html>") {
		t.Errorf("document w/ html but no head\n%s", noHead)
	}

	h := strings.TrimPrefix(card, "card-")
	if card == "card-" || title != "title-"+h || again != card || other == title {
		t.Fatalf("names: %s %s %s %s", card, title, again, other)
	}
	c, ti := "."+card, "."+title
	want := strings.Join([]string{
		c + " { background-color: #222; border-top-width: 2; padding: 1rem; }",
		c + " " + ti + " { margin: 0; }",
		c + ".big, " + c + ":focus { padding: 2rem; }",
		c + ":hover { border-color: gold; }",
		"@media (max-width: 600px) { " + c + " { padding: .5rem; } " + c + ":hover { color: red; } }",
		c + " img { width: 100%; }",
		ti + " { color: gold; }",
	}, "\n")
	if !strings.Contains(styles, want) {
		t.Errorf("styles\n%s\nwant\n%s", styles, want)
	}
	if strings.Count(styles, ti+" { color: gold; }") != 1 {
		t.Errorf("duplicate sheet\n%s", styles)
	}
	if rendered != `<p class="`+card+`"></p>` {
		t.Errorf("render (no styles): %s", rendered)
	}
	if !strings.Contains(doc, "<title>t</title><style data-midnight-css>") || !strings.Contains(doc, "</style></head>") {
		t.Errorf("document w/ head\n%s", doc)
	}
	if !strings.HasPrefix(bare, "<!DOCTYPE html><style data-midnight-css>") || !strings.HasSuffix(bare, "</style><p></p>") {
		t.Errorf("document w/o head\n%s", bare)
	}
}

// New styles after the page was sent need the whole page.
func TestMidnightMarkupCSSDispatch(t *testing.T) {
	L := NewState(Options{Midnight: midnightMarkupOptions})
	defer L.Close()
	source := midnightMarkupActionsPrelude + `local fancy = false
local Btn = () -> {
  local s = fancy && markup.css({ fancy = { color = 'gold' } }) || markup.css({ plain = { color = 'gray' } })
  return <button class={s.fancy || s.plain} onclick={() -> { fancy = !fancy }}>x</button>
}
local page = markup.document(markup.mount(() -> <main><Btn/></main>, { rpc = '/', morph = false }))
local id = ids(page)[1]
local first = markup.dispatch(id).body  -- new css: whole page
local second = markup.dispatch(id).body -- back to plain (already sent): fragment
local third = markup.dispatch(id).body  -- fancy again (already sent): fragment
return first, second, third`
	if err := L.DoMidnightString(source); err != nil {
		t.Fatal(err)
	}
	first, second, third := L.Get(-3).String(), L.Get(-2).String(), L.Get(-1).String()
	if !strings.HasPrefix(first, "<!DOCTYPE html><style data-midnight-css>") || !strings.Contains(first, "color: gold") {
		t.Errorf("first\n%s", first)
	}
	if !strings.HasPrefix(second, "<template") || !strings.HasPrefix(third, "<template") {
		t.Errorf("second / third\n%s\n%s", second, third)
	}
}
