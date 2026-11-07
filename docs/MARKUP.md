# Markup (`-X markup`)

An experimental extension for writing HTML in midnight, JSX style, and a small
runtime (`midnight.markup`) that renders it to HTML: components, scoped styles, and
(w/ a host that keeps state between requests, like
[rest](https://github.com/taybart/rest)'s `page` handlers) interactive pages.

```midnight
local { css, state } = require('midnight.markup')

local s = css({ counter = { display = 'flex', gap = '.5rem' } })

local Counter = (props) -> {
  local st = state({ count = props.start || 0 })
  return <div class={s.counter}>
    <button onclick={() -> { st.count -= 1 }}>-</button>
    <span>{st.count}</span>
    <button onclick={() -> { st.count += 1 }}>+</button>
  </div>
}

return () -> <html>
  <head><title>counters</title></head>
  <body><Counter/><Counter start={10}/></body>
</html>
```

- [Enabling it](#enabling-it)
- [Syntax](#syntax)
- [Rendering](#rendering)
- [Components](#components)
- [Scoped styles](#scoped-styles)
- [Interactive pages](#interactive-pages)
- [Runtime API](#runtime-api)
- [Portability](#portability)
- [Tooling](#tooling)

## Enabling it

```sh
midnight -X markup page.mn               # run
midnight compile -X markup .               # compile to lua (runs on any lua, see Portability)
midnight compile -X markup=harmony.h .     # w/ another factory (see below)
```

```go
L := lua.NewState(lua.Options{Midnight: parse.MidnightOptions{Markup: true}})
```

Experiments combine (`-X match,markup`, or `-X match -X markup`). Without `-X
markup`, `<` is always an operator. `jsx` (`-X jsx`, `require('midnight.jsx')`) is
the old name, and still works for now.

## Syntax

Elements are lowered to calls of `h(tag, props, ...children)`, like JSX's
classic runtime:

| midnight | lua |
|---|---|
| `<div class="card" id={id}>hi {name}</div>` | `h('div', { class = 'card', id = id }, 'hi ', name)` |
| `<input disabled/>` | `h('input', { disabled = true })` |
| `<Card {...props} title="x"/>` | `h(Card, <props merged left to right>)` |
| `<ui.Button/>` | `h(ui.Button, nil)` |
| `<>a {b}</>` | `h(nil, nil, 'a ', b)` |

- **Tags**: lowercase names, and names w/ `-` or `:` (`my-el`, `svg:path`),
  are HTML elements (strings). Capitalized and dotted names are components
  (expressions).
- **Attributes**: names can have `-`, `:`, `.`, and start w/ `@` or `:`
  (`hx-post`, `@click`, `x-on:click.prevent`). Values are midnight strings (so
  `class="btn {kind}"` interpolates), `{expressions}`, or nothing (`true`).
  `{...spread}` merges a table (nil is ignored).
- **Children**: text, `{expressions}`, and elements. Text is cleaned up like
  JSX: lines are trimmed, whitespace only lines are dropped, and the rest are
  joined w/ a space. HTML entities (`&nbsp;`, `&mdash;`, `&#123;`, ...) are
  decoded. `{}` and `{--[[ comments ]]}` are ignored (a `--` comment would
  run to the end of the line, past the `}`).
- **`<style>` and `<script>`** contain raw text, like HTML: no `{...}`
  expressions, whitespace cleanup, or entities, so css / js can be written as
  is.
- **Where elements start**: a `<` followed by a name (or `>`) starts an element
  wherever an expression can start, so after a name, number, string, `)`,
  `]`, or `}` it's still a comparison (`a < b`, `f()<2`, `t[1]<3`).
- An expression in `{...}` is truncated to one value (like parentheses).

### The factory

Elements call `h`: whatever `h` is in scope (ex. `local { h } =
require('midnight.markup')`). If there's no `h`, it's imported automatically (once, at
the top of the file, like JSX's automatic runtime), so files w/ elements don't
need any imports.

`-X markup=name` (`MidnightOptions.MarkupFactory`) uses another function instead, ex.
`-X markup=harmony.h` or `-X markup=React.createElement`.

## Rendering

`render(node)` renders to an HTML string, `document(node)` to a whole page
(w/ `<!DOCTYPE html>`, scoped styles, and the client script of interactive
pages).

- Text and attribute values are escaped. `raw(html)` is trusted HTML, rendered
  as is.
- `nil`, `false`, and `true` render nothing (`{cond && <b/>}`). Lists (ex. from
  `map`) render each item.
- Attributes are rendered sorted (so output is deterministic). `false` / `nil`
  attributes are left out. `class` can be a table (`{ 'btn', active = on }`),
  as can `style` (`{ color = 'red', ['font-size'] = '2em' }`). Functions are
  left out (unless they're actions, see below).
- Void elements (`<br>`, `<img>`, ...) can't have children. The text of
  `<script>` and `<style>` isn't escaped.

## Components

A component is a function from props to an element (or anything renderable).
`props.children` has its children.

```midnight
local Card = (props) -> <section class="card">
  <h2>{props.title}</h2>
  {props.children}
</section>

<Card title="hi"><p>body</p></Card>
```

Components run when they're rendered (top down, parents first), not when the
element is created. Calling one as a plain function (`Card(props)`) renders it
inline, as part of the caller.

### State

`state(init)` returns a table that's kept between renders of a component
instance (ex. across actions on an interactive page). `init` (a table, or a
function returning one) is only used the first time.

```midnight
local Counter = (props) -> {
  local s = state({ count = props.start || 0 })
  return <button onclick={() -> { s.count += 1 }}>{s.count}</button>
}
```

Instances are identified by their position in the tree (and which component
they are), so call `state` the same way on every render (not conditionally).
`{cond && <X/>}` keeps its place even when it renders nothing, so it doesn't
move the components after it. In lists, give items a `key` (`<Item
key={item.id}/>`) so their state follows them when the list changes. State of
components that are no longer rendered is dropped.

## Scoped styles

`css(rules)` turns lua tables into css, w/ class names generated from a hash of
the rules, and returns the class names by name:

```midnight
local s = css({
  card = {
    padding = '1rem',
    backgroundColor = '#222',              -- or background_color
    ['&:hover'] = { borderColor = 'gold' },  -- `&` is the class
    ['&.big, &:focus'] = { padding = '2rem' },
    ['$title'] = { margin = 0 },           -- `$name` is another class from this css()
    img = { width = '100%' },              -- descendants
    ['@media (max-width: 600px)'] = { padding = '.5rem' },
  },
  title = { color = 'gold' },
})

<section class={s.card}><h2 class={s.title}>hi</h2></section>
```

- Property names in camelCase or snake_case become kebab-case. Values are used
  as is (`2` is `2`, not `2px`).
- Class names (ex. `card-1x9f3k`) only depend on the rules, so they're the same
  in every state and process.
- `document` puts all the styles in a `<style>` in the `<head>` (adding one to
  an `<html>` w/o it, or at the start of a page w/o `<html>`). `styles()` returns them, to put them somewhere else (ex. in a shadow
  root, w/ `render`).
- Call `css` at the top of a module (it's cheap to call in a component too:
  the same rules are only added once). For per-instance values, use the
  `style` attribute.

## Interactive pages

On a mounted page, functions in `on*` attributes (`onclick`, `onClick`, and
`on:click` are all `click`) become actions: the event calls the server, which
runs the function and sends back what changed.

```midnight
local { mount, dispatch, document } = require('midnight.markup')

-- the page
document(mount(() -> <App/>, { rpc = '/actions', headers = { ['X-State'] = id } }))

-- the rpc endpoint (POST, json body { id, data }); returns { headers, body }
dispatch(body.id, body.data)
```

W/o `rpc`, actions POST to the page's own url (`location.pathname +
location.search`, as the browser sees it), so the host can handle them w/
the same route as the page, even behind a proxy that rewrites paths.

The host has to run the page and its actions in the same lua state (rest's
`page` handlers do all of this, see rest's docs). From there:

- **Events**: an action gets a summary of the browser event: `{ type, value,
  checked, key, form }` (`value` of inputs, `checked` of checkboxes / radios,
  `key` of keyboard events, and the fields of a submitted `form`). Submitting a
  form doesn't navigate.
- **Updates**: after an action, the page is rendered again, and only the
  components whose own markup changed are sent (as `<template
  data-midnight-target="id">`, matched to `data-midnight` attributes on the page). The
  whole page is sent when something outside of a component changed, when the
  changed component's root is `<html>` / `<head>` / `<body>`, or when there are
  new styles. Nothing changed is an empty response.
- **Morphing**: updates are applied w/
  [idiomorph](https://github.com/bigskysoftware/idiomorph) (vendored as
  `midnight.markup.idiomorph`, and inlined in the page), so focus, selection, and
  what's being typed survive. `mount`'s `morph` option can be a url to load it
  from instead, or `false` to replace elements.
- **Ordering and errors**: actions are sent one at a time, in order. A 410
  response (ex. the server's state for the page is gone) reloads the page.
- Pages w/o any actions are static: no client script (`isInteractive()` is
  false).

## Runtime API

`require('midnight.markup')`:

| | |
|---|---|
| `h(tag, props, ...)` | element (`tag` string), component (function), or fragment (nil) |
| `render(node)` | HTML string |
| `document(node)` | `<!DOCTYPE html>` page, w/ styles (and the client script of interactive pages) |
| `raw(html)` | trusted HTML, rendered as is |
| `map(list, fn)` | list of `fn(value, index)` |
| `escape(s)` | HTML escaped string |
| `isElement(v)` | whether `v` was built by `h` |
| `state(init)` | state of the component being rendered |
| `css(rules)` | scoped styles, returns class names |
| `styles()` | all styles, as a `<style>` (or `''`) |
| `mount(root, { rpc, headers, morph })` | makes `root()` the interactive page (actions POST to `rpc`, or the page's own url), returns `root()` |
| `dispatch(id, data)` | runs an action, returns `{ headers, body }` (what changed) |
| `isInteractive()` | whether the last render of the mounted page had actions |

## Portability

Only the syntax is midnight specific. The runtime is plain lua (5.1 to 5.5,
LuaJIT), so `midnight compile -X markup` output runs anywhere w/ the runtime on
`package.path`:

```
midnight/markup.lua            -- lib/markup.lua
midnight/markup/idiomorph.lua  -- lib/idiomorph.lua (optional: w/o it, pages load idiomorph from a cdn)
```

midnight preloads both (and compiles them once per process).

## Tooling

- `midnight lint -X markup` and `midnight fmt -X markup` support it (`fmt` keeps elements as
  written).
- [tree-sitter-midnight](https://github.com/taybart/tree-sitter-midnight) always parses
  it (w/ highlights, indents, and css / js injected into `<style>` /
  `<script>`).
