# Pages

`github.com/taybart/midnight/pages` serves midnight markup pages as
`http.Handler`s, so any Go app can mount them on its router (net/http, gin,
fiber, chi, ...). A page renders on `GET`, its actions (functions in `on*`
attributes) run on the server, and only the components that changed are sent
back. See [MARKUP.md](MARKUP.md) for the markup syntax and runtime.

Page scripts get midnight's stdlib, and what the app adds (see
[hooks](#hooks)). [rest](https://github.com/taybart/rest) serves the same
`.mn` files with its `page` handlers.

```go
import "github.com/taybart/midnight/pages"

eng := pages.New(pages.Options{})
defer eng.Close()

mux := http.NewServeMux()
mux.Handle("/app", eng.File("ui/app.mn",
    pages.Props(func(r *http.Request) (any, error) {
        return map[string]any{"name": r.URL.Query().Get("name")}, nil
    }),
    pages.Funcs("app.db", map[string]any{
        "list": db.List, // func(ctx context.Context) ([]Item, error)
        "add":  db.Add,  // func(ctx context.Context, name string) error
    }),
))
```

```midnight
-- ui/app.mn
local { state, map } = require('midnight.markup')
local db = require('app.db')

local Items = () -> {
  local st = state({ error = nil })
  local add = (e) -> {
    local ok, err = pcall(db.add, e.form.name)
    st.error = if ok { nil } else { err }
  }
  return <div>
    <form onsubmit={add}><input type="text" name="name"/></form>
    {st.error && <p>{st.error}</p>}
    <ul>{map(db.list(), (it) -> <li>{it.name}</li>)}</ul>
  </div>
}

return (props) -> <html>
  <body>
    <h1>hello {props.name || 'stranger'}</h1>
    <Items/>
  </body>
</html>
```

A page returns a component (called with the [props](#props)), a markup element,
or a response table (ex. a redirect). The element it
returns is rendered once: actions re-render components, so anything that
changes (ex. `data-theme` on `<body>`) has to be in one.

The [example](../examples/pages) is a todo app on net/http and fiber, with
embedded files, Go functions, and tests that drive the page like a browser.

### Mounting

A page handler renders on `GET` (and `HEAD`) and runs actions on `POST`, so
mount it for both methods (other methods get a `405`). The app can't have its
own `POST` on the same path.

```go
mux.Handle("/app", h)                              // net/http (no method in the pattern)
mux.Handle("/app/", http.StripPrefix("/app", h))   // under a prefix
r.Any("/app", gin.WrapH(h))                        // gin
app.All("/app", adaptor.HTTPHandler(h))            // fiber (github.com/gofiber/fiber/v3/middleware/adaptor)
```

- Actions POST to the url the browser shows (path and query), so the page
  doesn't need to know its route: prefixes, router groups and proxies just
  work. `pages.ActionURL("/rpc/app")` sends them somewhere else (which has to
  reach the same handler).
- The router's middleware wraps the handler, so it runs for actions too (ex.
  auth, logging, compression). Use the router's own for gzip etc., pages
  doesn't have its own.
- Fiber runs on fasthttp, so `adaptor.HTTPHandler` converts each request and
  response.
- Errors in a page's scripts are a `500` (the error isn't sent, it can have
  source in it), and are passed to `Options.OnError` (logged by default).
- Each interactive page load gets its own lua state, kept for its actions
  (its id is in the client's `X-Midnight-State` header). Unused states are
  closed after `StateTTL`, or the least recently used once there are
  `MaxStates`, and a `410` for a closed one makes the page reload. Pages without actions are
  static: no client script, and no state is kept.

### Where pages come from

| | |
|---|---|
| `eng.File(path, opts...)` | a file on disk. `require` looks next to it first (`require('header')` is `header.mn`, `.midnight`, or `.lua` in its directory), then in `Options.Path`. Changes show up on the next page load. |
| `eng.FS(fsys, name, opts...)` | a file in an `fs.FS`, ex. an `embed.FS`. Compiled once and kept. |
| `eng.Source(src, opts...)` | midnight source. `require` uses `Options.Path`. |

`FS` is for files built into the binary:

```go
//go:embed ui
var ui embed.FS

mux.Handle("/app/", http.StripPrefix("/app", eng.FS(ui, "ui/app.mn")))
```

- `require` only looks in the fs, next to the page: `require('header')` is
  `ui/header.mn` (or `.midnight`, `.lua`, or `ui/header/init.*`), and
  `require('parts.nav')` is `ui/parts/nav.mn`. Not on disk, and not in
  `Options.Path`, so the binary runs the files it was built with, from any
  directory. The `Funcs` / `LuaFuncs` modules and midnight's own still work.
- Files are found and compiled on first use, and kept: later page loads
  don't read the fs at all. So changes to the fs aren't seen; use `File`
  for files that change.
- Paths are `fs.FS` paths (`ui/app.mn`, not `./ui/app.mn`). A bad path, a
  missing file, or a directory panics, so a typo fails at startup.

To edit embedded files while the app runs, serve them with `File` in
development (the example's `-ui` flag does this):

```go
if dev {
    h = eng.File("ui/app.mn", opts...)
} else {
    h = eng.FS(ui, "ui/app.mn", opts...)
}
```

Static files next to a page (css, images, ...) are the router's: ex.
`http.FileServerFS(ui)` / `http.ServeFileFS` for net/http, or `c.SendFile`
with `fiber.SendFile{FS: ui}` on fiber.

### Props

`pages.Props(fn)` gives a page props, by the request that loads it. A page
that returns a component (`return (props) -> <html>...</html>`) is called with
them. They're converted to lua like `Funcs` results (structs become tables,
see [the types](#types)).

```go
pages.Props(func(r *http.Request) (any, error) {
    user, err := auth.User(r)
    if err != nil {
        return nil, err // a 500
    }
    return PageProps{User: user, Tab: r.URL.Query().Get("tab")}, nil
})
```

Props are only for the page load. Actions are later requests, so to get
data then (or to change it), use Go functions.

#### Data for the page's js (`pages.JSON`)

`pages.JSON(v)` encodes `v` with `encoding/json` for the page's own js. In the
page it's opaque (it can be rendered, not read): it goes from Go to the
browser as is, without becoming lua tables, so its types are Go's (an empty map
is `{}`, not `[]`) and big data costs the page nothing.

```go
pages.Props(func(r *http.Request) (any, error) {
    return map[string]any{"repos": pages.JSON(repos)}, nil
})
```

Render it in a JSON `<script>` (or `<Data/>`, the same thing), or declare it
in a script with `define:vars`:

```midnight
<Data id="repos" value={props.repos}/>
-- JSON.parse(document.getElementById('repos').textContent)

<script define:vars={{ repos = props.repos }}>
  const names = repos.map(r => r.name)
</script>
```

The JSON never has a `<`, `>`, or `&`, so nothing in the data can close the
`<script>`. An encoding error (ex. a channel in `v`) is the page's error (a
500). Lua values work too: midnight's `json(v)` encodes them, and a JSON
`<script>`'s children are encoded when it's rendered.

### Go functions (`Funcs`)

`pages.Funcs(module, fns)` makes plain Go functions a module of the page, so
it can call into the app (databases, services, ...) when it renders and in
its actions:

```go
pages.Funcs("app.todos", map[string]any{
    "list":   store.List,   // func(ctx context.Context) ([]Todo, error)
    "add":    store.Add,    // func(ctx context.Context, title string) (Todo, error)
    "toggle": store.Toggle, // func(ctx context.Context, id int) error
})
```

```midnight
local todos = require('app.todos')

local list = todos.list()               -- a table of Todos (by json tag)
local ok, err = pcall(todos.add, title) -- Go errors are lua errors
```

- A first param of `context.Context` or `*http.Request` isn't an argument:
  it gets the request being handled. That's the action's request for code
  that runs in an action (so its context, cookies, auth headers, ...), not
  the page load's.
- Results can be `()`, `(T)`, `(error)`, or `(T, error)`. A non-nil error is
  raised as a lua error, `pcall`-able, with the module and function in the
  message: `app.todos.add: title is required`.
- A missing arg is `nil` (an error for a non-pointer type), too many args
  is an error.
- Bad args are lua errors that say where: `app.todos.save: arg 1[2].name:
  expected string, got number`.
- Methods work (`store.List`), so do closures.

The function's signature is checked when the handler is built: an
unsupported one (ex. a `chan` param, or two non-error results) panics, with the
module, function, and param, so it fails at startup, not on a click.

All the reflection on a signature (and its types) happens then too. A call
converts its args, calls the function (`reflect.Value.Call`), and converts
its result: about 150-350ns, vs 35-55ns for a [`LuaFuncs`](#raw-lua-functions-luafuncs)
function. A whole action (dispatch, render, diff) is about 40µs, so it's
rarely worth the difference.

#### Types

| Go | midnight |
|---|---|
| `string`, `[]byte` | string |
| `bool` | boolean |
| ints, uints, floats | number (into an int: only integers in range) |
| `[]T` | array table (a nil slice is `{}`) |
| `map[string]T` | table (a nil map is `{}`) |
| `struct`, `*struct` | table by field name, or its `json` tag (`json:"-"` and unexported fields skipped, embedded structs flattened, `omitempty` / `omitzero` fields left out when empty, like `encoding/json`) |
| `time.Time` | string (RFC 3339) |
| `any` | by the value's type (into Go: `string`, `float64`, `bool`, `[]any`, `map[string]any`, or `nil`) |
| `*T` | `nil` or `T` |

Recursive types work. Channels, funcs, complex numbers, non-string map
keys, and interfaces with methods aren't supported.

Midnight's defaults (`local { q = '?' } = props`) only apply to `nil`, and a
Go zero value (`""`, `0`, `false`) isn't one. Tag a field whose zero value
means "not set" `omitempty` (or `omitzero`), and it's `nil` in lua when it's
empty, so the default applies.

### Raw lua functions (`LuaFuncs`)

For full control, or a hot path, `pages.LuaFuncs(module, fns)` makes plain
gopher-lua functions (`func(l *lua.LState) int`, `lua` being
`github.com/taybart/midnight`) a module. They get their args from the stack,
push their results, and raise errors with `l.RaiseError` / `l.ArgError`.
`pages.Request(l)` is the request being handled (the action's, in an
action):

```go
pages.LuaFuncs("app.user", map[string]lua.LGFunction{
    "name": func(l *lua.LState) int {
        user := auth.FromContext(pages.Request(l).Context())
        l.Push(lua.LString(user.Name))
        return 1
    },
})
```

An `LGFunction` in a `Funcs` map is used as is too, to mix them in one
module.

### Hooks

Two engine options run Go on a page's lua state, ex. to add globals or
modules: `Options.BeforeRequest(l, r)` runs for every request a page state
handles (its load, then each action), and `Options.Setup(l, r)` once per new
state, after it. `pages.Setup(fn)` is the same for one page (after the
engine's). `pages.Request(l)` and `pages.StateID(l)` are the request being
handled and the state's id.

### Caching (`Cache`)

`pages.Cache(ttl, vary...)`: a page that's the same for every visitor is rendered once per
`ttl`, and served from memory.

```go
eng.FS(ui, "ui/about.mn", pages.Cache(5*time.Minute, "Accept-Language"))
```

- Responses are kept by path, query, and the `vary` request headers: a
  promise that the page only depends on those (pages can't tell what a
  page's scripts or Go functions read).
- Only `200`s that don't set cookies are kept. Interactive pages are never
  cached (each one has its visitor's state id); a warning is logged once.
- Responses get `Cache-Control: public, max-age=<ttl>` unless the page sets
  its own.
- At most 1000 responses / 64MB per page, the oldest are dropped first.
- Router middleware runs for cached responses too (it's outside of the
  handler).

### Engine options

```go
eng := pages.New(pages.Options{
    MaxStates: 1000,             // kept page states, shared by every page (default 1000)
    StateTTL:  30 * time.Minute, // close states unused this long (default 30m)
    Path:      "./lib/?.mn;./lib/?.lua", // more places for require (not used by FS pages)
    BeforeRequest: func(l *lua.LState, r *http.Request) error { ... }, // every request (see hooks)
    Setup:         func(l *lua.LState, r *http.Request) error { ... }, // every new page state
    OnError: func(r *http.Request, err error) { // errors from page scripts (default: log)
        slog.Error("page", "path", r.URL.Path, "err", err)
    },
})
defer eng.Close() // closes the kept states
```

An app usually has one engine: its pages share the state store, so
`MaxStates` limits the memory of all of them (states are roughly 0.3-2MB
each).

| Page option | |
|---|---|
| `Props(fn)` | the page's props, by request |
| `Funcs(module, fns)` | Go functions as a module |
| `LuaFuncs(module, fns)` | gopher-lua functions as a module |
| `Setup(fn)` | called for each new state of the page |
| `ActionURL(url)` | where actions POST (default: the page's url) |
| `Cache(ttl, vary...)` | cache rendered responses |
