# pages example: todos

A todo list served by `github.com/taybart/midnight/pages` on two routers: net/http's
`ServeMux` (`cmd/std`) and fiber v3 (`cmd/fiber`), and done client side with
custom elements (`cmd/elements`). It's its own module, so fiber isn't a
dependency of midnight; it builds against this checkout (`replace` in go.mod).

```sh
cd examples/pages
go run ./cmd/std   # http://localhost:8090/todos/
go run ./cmd/fiber # http://localhost:8091/todos/
go run ./cmd/elements # http://localhost:8092/todos/
go test ./...
```

- `todo/ui/`: the page, a component per file, each with its own scoped styles
  (midnight's `css`): `app.mn` (the `<html>` shell, layout, theme),
  `header.mn`, `list.mn` (form, error, counts), and `item.mn` (one
  todo). `require('list')` etc. find them: file pages look in their own
  directory. Their actions (`onsubmit`, `onchange`, `onclick`) run on the
  server, and only what changed is sent back.
- `todo/ui/tb.css`: [tb.css](https://github.com/taybart/tb.css) (vendored),
  the base styles (dark / light theme, buttons, inputs, lists). The page links
  it relatively, and each router serves it next to the page (`todo.Styles`
  on the std mux, `c.SendFile` on fiber). The app's own styles are scoped
  (midnight's `css`), and the theme toggle is page state (`data-theme` on body).
- `todo/page.go`: the page handler, with the store's methods as the
  `app.todos` module (`pages.Funcs`): plain Go methods, with the request's
  context, converted args / results, and errors as lua errors.
- `cmd/std`: mounted under `/todos/` with `http.StripPrefix`; `cmd/fiber`: in a
  `/todos` group. The handler is mounted for GET (render) and POST (actions):
  actions POST to the url the browser sees, so prefixes just work.
- fiber runs on fasthttp, not net/http, so the page (an `http.Handler`) goes
  through `adaptor.HTTPHandler`, which converts every request and response.
  Its app isn't an `http.Handler` either: its test serves it on a listener.
- `todo/todotest`: drives the page like its client script does; both routers
  are tested with it.

## Client side, with custom elements (`cmd/elements`)

The same app, with the behavior in the browser: the server renders the page
once (with the todos at the time), and plain js [custom
elements](https://developer.mozilla.org/en-US/docs/Web/API/Web_components/Using_custom_elements)
take it from there.

- `todo/ui/client.mn`: the page. It has no actions (no functions in `on*`
  attributes), so the server keeps no state for it and sends no client
  script. It reuses `header.mn` (without `on_toggle`, its button is plain), and
  renders the list's markup inside `<TodoList>`, with a `<template>` of one
  todo for the ones added in the browser.
- `todo/ui/elements.mn`: midnight's `elements('./elements.js', { TodoList =
  'todo-list', ThemeToggle = 'theme-toggle' })`: components for the
  elements, by their exported names. A page that uses one gets a module
  script in its `<head>` that imports `elements.js` and registers them.
- `todo/ui/elements.js`, `todo/ui/elements/*.js`: the elements. They wire
  up the markup the server rendered (event delegation on `<todo-list>`, the
  header's button in `<theme-toggle>`). Scoped class names are hashed, so
  the one `<todo-list>` toggles is an attribute (`done-class={s.done}`).
- `todo/api.go`: the store as a JSON api, which `<todo-list>` calls; each
  change responds with the whole list, and the element rebuilds it from the
  template. `todo.Scripts` serves the `.js` files next to the page (and
  nothing else in `todo/ui`).

Compared to `cmd/std`: no round trip to re-render, and no server state per
page, but the item markup lives twice (in `client.mn` and, as a clone of its
template, in the js) and the js has to keep the counts in sync itself.

The page's files (`todo/ui`) are embedded in the binary and served with
`eng.FS`: compiled once, nothing read from disk. To edit them while it runs,
point `-ui` at them (`go run ./cmd/std -ui todo/ui`): that serves them with
`eng.File`, and changes show up on the next page load.
