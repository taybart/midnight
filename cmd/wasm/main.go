//go:build js && wasm

// Command wasm is midnight for the browser (ex. a playground). It sets these
// globals:
//
//	midnightRun(src, onOutput)                 -> { lua, html, interactive, error }
//	midnightDispatch(id, data, onOutput)       -> { html, error }
//	midnightCompile(src)                       -> { lua, error }
//	midnightTokens(src)                        -> [[kind, text], ...]
//	midnightHighlight(src, lang)               -> [[class, text], ...] (or null)
//	midnightLint(src)                          -> [[line, message, rule], ...]
//	midnightFormat(src)                        -> { code, error }
//
// midnightLint reports what `midnight lint` would, and a syntax error as the
// rule "syntax". midnightFormat is `midnight fmt`.
//
// midnightRun runs src in a fresh state. What it prints (print, io.write) is
// passed to onOutput(text) as it's written, in chunks. If src returns a
// markup element (or a component), html is it rendered as a page.
//
// A page w/ actions (ex. onclick={...}) is interactive: its state is kept
// (until the next run), and its client script POSTs actions to RPCPath.
// Hand those to midnightDispatch, which runs the action and returns what
// changed, like a rest `page` handler would.
//
// A run (or an action) can loop forever: Go in wasm can't interrupt itself,
// so run it in a worker that can be terminated.
package main

import (
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"time"

	lua "github.com/taybart/midnight"
	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/lint"
	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

const (
	chunkName = "playground.mn"
	// where an interactive page's client script sends its actions
	rpcPath = "/__midnight_rpc__"
	// what a run can print, so a runaway loop can't flood the page
	maxOutput = 1 << 20
	// a line w/ this isn't linted (as cmd/midnight)
	lintIgnoreComment = "midnight-lint: ignore"
)

// page is the state of the last run, while its page is interactive.
var page struct {
	L        *lua.LState
	out      *output
	dispatch *lua.LFunction
}

// helpers runs in each state: it mounts what the chunk returned as a page,
// and dispatches its actions.
const helpers = `
local m = require('midnight.markup')
local function render(v)
  local root
  if type(v) == 'function' then
    root = function() return m.h(v, {}) end -- a component
  elseif m.isElement(v) then
    root = function() return v end -- (its components render again each time)
  else
    return nil
  end
  local html = m.document(m.mount(root, { rpc = '` + rpcPath + `' }))
  return html, m.isInteractive()
end
local function dispatch(id, data)
  return m.dispatch(id, data).body
end
return render, dispatch
`

func main() {
	js.Global().Set("midnightRun", js.FuncOf(func(this js.Value, args []js.Value) any {
		return run(args[0].String(), callback(args, 1))
	}))
	js.Global().Set("midnightDispatch", js.FuncOf(func(this js.Value, args []js.Value) any {
		return dispatch(args[0].String(), args[1], callback(args, 2))
	}))
	js.Global().Set("midnightCompile", js.FuncOf(func(this js.Value, args []js.Value) any {
		src := args[0].String()
		chunk, err := parse.ParseMidnight(strings.NewReader(src), chunkName)
		if err != nil {
			return map[string]any{"error": err.Error()}
		}
		comments, _ := parse.MidnightDocComments(src, chunkName)
		return map[string]any{"lua": luagen.GenerateWithComments(chunk, comments)}
	}))
	js.Global().Set("midnightTokens", js.FuncOf(func(this js.Value, args []js.Value) any {
		src := args[0].String()
		spans, _ := parse.MidnightHighlight(src) // (partial on an error, which is fine while typing)
		toks := make([]any, len(spans))
		for i, s := range spans {
			toks[i] = []any{s.Kind, src[s.Start:s.End]}
		}
		return toks
	}))
	// classed spans for coloring (see parse.HighlightCode), lang 'midnight' or
	// 'lua', null for others
	js.Global().Set("midnightHighlight", js.FuncOf(func(this js.Value, args []js.Value) any {
		src, lang := args[0].String(), "midnight"
		if len(args) > 1 && args[1].Type() == js.TypeString {
			lang = args[1].String()
		}
		spans, ok := parse.HighlightCode(lang, src)
		if !ok {
			return nil
		}
		toks := make([]any, len(spans))
		for i, s := range spans {
			toks[i] = []any{s.Class, src[s.Start:s.End]}
		}
		return toks
	}))
	js.Global().Set("midnightLint", js.FuncOf(func(this js.Value, args []js.Value) any {
		return safely(func() any { return lintSource(args[0].String()) })
	}))
	js.Global().Set("midnightFormat", js.FuncOf(func(this js.Value, args []js.Value) any {
		return safely(func() any {
			out, err := format.Format([]byte(args[0].String()), format.Options{})
			if err != nil {
				return map[string]any{"error": err.Error()}
			}
			return map[string]any{"code": string(out)}
		})
	}))
	select {}
}

// safely runs fn, turning a panic into { error }. (These run in the page's
// instance, as you type: a panic would end it, and highlighting w/ it.)
func safely(fn func() any) (res any) {
	defer func() {
		if r := recover(); r != nil {
			res = map[string]any{"error": fmt.Sprint(r)}
		}
	}()
	return fn()
}

// lintSource returns src's problems: [line, message, rule], by line.
func lintSource(src string) []any {
	lines := strings.Split(src, "\n")
	chunk, err := parse.ParseMidnight(strings.NewReader(src), chunkName)
	if err != nil {
		line, msg := 0, err.Error()
		var perr *parse.Error
		if errors.As(err, &perr) {
			line, msg = perr.Pos.Line, strings.TrimSpace(perr.Message)
			if line == parse.EOF {
				line = len(strings.Split(strings.TrimRight(src, "\n"), "\n"))
			}
		}
		return []any{[]any{line, msg, "syntax"}}
	}
	problems := []any{}
	for _, d := range lint.Check(chunk, lint.Options{}) {
		if d.Line >= 1 && d.Line <= len(lines) && strings.Contains(lines[d.Line-1], lintIgnoreComment) {
			continue
		}
		problems = append(problems, []any{d.Line, d.Message, d.Rule})
	}
	return problems
}

func callback(args []js.Value, i int) js.Value {
	if len(args) > i && args[i].Type() == js.TypeFunction {
		return args[i]
	}
	return js.Undefined()
}

// output batches what a run prints, so a chatty loop is a few messages a
// second instead of one per print.
type output struct {
	L         *lua.LState
	fn        js.Value
	buf       strings.Builder
	total     int
	lastFlush time.Time
}

func (o *output) write(s string) {
	o.total += len(s)
	if o.total > maxOutput {
		o.flush()
		o.L.RaiseError("output limit reached (%d KB), stopping", maxOutput>>10)
	}
	o.buf.WriteString(s)
	if o.buf.Len() > 4096 || time.Since(o.lastFlush) > 50*time.Millisecond {
		o.flush()
	}
}

func (o *output) flush() {
	if o.buf.Len() > 0 && o.fn.Type() == js.TypeFunction {
		o.fn.Invoke(o.buf.String())
	}
	o.buf.Reset()
	o.lastFlush = time.Now()
}

// reset starts a new run (or action) of the same state.
func (o *output) reset(fn js.Value) {
	o.fn, o.total, o.lastFlush = fn, 0, time.Now()
	o.buf.Reset()
}

func closePage() {
	if page.L != nil {
		page.L.Close()
	}
	page.L, page.out, page.dispatch = nil, nil, nil
}

func newState(onOutput js.Value) (*lua.LState, *output) {
	L := lua.NewState()
	out := &output{L: L}
	out.reset(onOutput)

	L.SetGlobal("print", L.NewFunction(func(L *lua.LState) int {
		var b strings.Builder
		for i := 1; i <= L.GetTop(); i++ {
			if i > 1 {
				b.WriteByte('\t')
			}
			b.WriteString(L.ToStringMeta(L.Get(i)).String())
		}
		b.WriteByte('\n')
		out.write(b.String())
		return 0
	}))
	if io, ok := L.GetGlobal("io").(*lua.LTable); ok {
		io.RawSetString("write", L.NewFunction(func(L *lua.LState) int {
			for i := 1; i <= L.GetTop(); i++ {
				out.write(L.ToStringMeta(L.Get(i)).String())
			}
			return 0
		}))
	}
	if os, ok := L.GetGlobal("os").(*lua.LTable); ok {
		os.RawSetString("exit", lua.LNil) // (it would end the whole wasm instance)
	}
	return L, out
}

func run(src string, onOutput js.Value) map[string]any {
	closePage()
	res := map[string]any{}

	chunk, err := parse.ParseMidnight(strings.NewReader(src), chunkName)
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	comments, _ := parse.MidnightDocComments(src, chunkName)
	res["lua"] = luagen.GenerateWithComments(chunk, comments)

	L, out := newState(onOutput)
	keep := false
	defer func() {
		out.flush()
		if !keep {
			L.Close()
		}
	}()

	proto, err := lua.Compile(chunk, chunkName)
	if err != nil {
		res["error"] = err.Error()
		return res
	}
	L.Push(L.NewFunctionFromProto(proto))
	if err := L.PCall(0, 1, nil); err != nil {
		res["error"] = err.Error()
		return res
	}
	result := L.Get(-1)
	L.Pop(1)

	// render it, if it's markup
	if err := L.DoString(helpers); err != nil {
		res["error"] = err.Error()
		return res
	}
	render, dispatchFn := L.Get(-2), L.Get(-1)
	L.Pop(2)
	L.Push(render)
	L.Push(result)
	if err := L.PCall(1, 2, nil); err != nil {
		res["error"] = err.Error()
		return res
	}
	html, interactive := L.Get(-2), lua.LVAsBool(L.Get(-1))
	L.Pop(2)
	if html == lua.LNil {
		return res
	}
	res["html"] = html.String()
	res["interactive"] = interactive

	if interactive {
		keep = true
		page.L, page.out = L, out
		page.dispatch, _ = dispatchFn.(*lua.LFunction)
	}
	return res
}

// dispatch runs an action of the interactive page, w/ the event data its
// client script sent, and returns what changed (see markup's dispatch).
func dispatch(id string, data js.Value, onOutput js.Value) map[string]any {
	if page.L == nil || page.dispatch == nil {
		return map[string]any{"error": "the page isn't running anymore (run it again)"}
	}
	L := page.L
	page.out.reset(onOutput)
	defer page.out.flush()

	L.Push(page.dispatch)
	L.Push(lua.LString(id))
	L.Push(toLua(L, data))
	if err := L.PCall(2, 1, nil); err != nil {
		return map[string]any{"error": err.Error()}
	}
	html := L.Get(-1).String()
	L.Pop(1)
	return map[string]any{"html": html}
}

// toLua converts an event's summary (plain JS data: objects, arrays,
// strings, numbers, and booleans) to a lua value. (Not encoding/json: it
// makes the wasm about a quarter bigger.)
func toLua(L *lua.LState, v js.Value) lua.LValue {
	switch v.Type() {
	case js.TypeBoolean:
		return lua.LBool(v.Bool())
	case js.TypeNumber:
		return lua.LNumber(v.Float())
	case js.TypeString:
		return lua.LString(v.String())
	case js.TypeObject:
		if js.Global().Get("Array").Call("isArray", v).Bool() {
			n := v.Length()
			t := L.CreateTable(n, 0)
			for i := 0; i < n; i++ {
				t.Append(toLua(L, v.Index(i)))
			}
			return t
		}
		keys := js.Global().Get("Object").Call("keys", v)
		t := L.CreateTable(0, keys.Length())
		for i := 0; i < keys.Length(); i++ {
			k := keys.Index(i).String()
			t.RawSetString(k, toLua(L, v.Get(k)))
		}
		return t
	}
	return lua.LNil
}
