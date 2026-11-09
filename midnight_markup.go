package lua

// midnight: runtime library for markup elements (see parse/midnight_markup.go),
// preloaded as `midnight.markup` (and its vendored morph library as
// `midnight.markup.idiomorph`).

import (
	_ "embed"
	"strings"
	"sync"

	"github.com/taybart/midnight/parse"
)

// MarkupLibName is the module name of the markup runtime (`require('midnight.markup')`).
const MarkupLibName = "midnight.markup"

// MarkupLibOldName is the old (deprecated) name of the markup runtime, the
// same module as MarkupLibName.
const MarkupLibOldName = "midnight.jsx"

// MarkupMorphLibName is the module name of the vendored idiomorph (the script, as
// a string), which midnight.markup inlines in interactive pages.
const MarkupMorphLibName = "midnight.markup.idiomorph"

//go:embed lib/markup.lua
var MarkupLibSource string

//go:embed lib/idiomorph.lua
var MarkupMorphLibSource string

// An embedded lua module. It's compiled once, and its (immutable) bytecode is
// shared by every state that requires it.
type embeddedLib struct {
	name, source string
	// native returns the module's arg (`...`), if any
	native func(*LState) LValue
	once   sync.Once
	proto  *FunctionProto
	err    error
}

func (lib *embeddedLib) compiled() (*FunctionProto, error) {
	lib.once.Do(func() {
		chunk, err := parse.Parse(strings.NewReader(lib.source), lib.name)
		if err != nil {
			lib.err = err
			return
		}
		lib.proto, lib.err = Compile(chunk, lib.name)
	})
	return lib.proto, lib.err
}

func (lib *embeddedLib) preload(L *LState) {
	L.PreloadModule(lib.name, func(L *LState) int {
		proto, err := lib.compiled()
		if err != nil {
			L.RaiseError("%s", err.Error())
		}
		L.Push(L.NewFunctionFromProto(proto))
		if lib.native != nil {
			L.Push(lib.native(L))
			L.Call(1, 1)
		} else {
			L.Call(0, 1)
		}
		return 1
	})
}

// markupNative is the Go helpers of the markup runtime (`...` in
// lib/markup.lua, which has lua versions of each).
func markupNative(L *LState) LValue {
	native := L.NewTable()
	native.RawSetString("join", L.NewFunction(markupJoin))
	native.RawSetString("shell", L.NewFunction(markupShell))
	return native
}

// markupJoin(buf, skip) joins a render's buffer (strings, and child
// components' buffers) into html, w/o the `data-midnight` attributes of the
// buffers in skip (if it's a table: buffer -> the attribute's index). See
// flatten in lib/markup.lua.
func markupJoin(L *LState) int {
	buf := L.CheckTable(1)
	skip, _ := L.Get(2).(*LTable)
	var b strings.Builder
	joinInto(&b, buf, skip)
	L.Push(LString(b.String()))
	return 1
}

// markupShell(buf, children) joins a buffer w/ its child components' buffers
// (at the indices in children: index -> component id) replaced by
// `<!--id-->`. See shell in lib/markup.lua.
func markupShell(L *LState) int {
	buf, children := L.CheckTable(1), L.CheckTable(2)
	var b strings.Builder
	n := buf.Len()
	for i := 1; i <= n; i++ {
		if id, ok := children.RawGetInt(i).(LString); ok {
			b.WriteString("<!--")
			b.WriteString(string(id))
			b.WriteString("-->")
			continue
		}
		switch part := buf.RawGetInt(i).(type) {
		case LString:
			b.WriteString(string(part))
		case LNumber:
			b.WriteString(part.String())
		}
	}
	L.Push(LString(b.String()))
	return 1
}

func joinInto(b *strings.Builder, buf, skip *LTable) {
	attr := -1
	if skip != nil {
		if i, ok := skip.RawGet(buf).(LNumber); ok {
			attr = int(i)
		}
	}
	n := buf.Len()
	for i := 1; i <= n; i++ {
		switch part := buf.RawGetInt(i).(type) {
		case LString:
			if i != attr {
				b.WriteString(string(part))
			}
		case *LTable:
			joinInto(b, part, skip)
		case LNumber:
			b.WriteString(part.String())
		}
	}
}

var markupLibs = []*embeddedLib{
	{name: MarkupLibName, source: MarkupLibSource, native: markupNative},
	{name: MarkupMorphLibName, source: MarkupMorphLibSource},
}

// OpenMarkup registers the markup runtime in package.preload, so it's loaded (once
// per state) by `require('midnight.markup')`.
func OpenMarkup(L *LState) int {
	for _, lib := range markupLibs {
		lib.preload(L)
	}
	L.PreloadModule(MarkupLibOldName, func(L *LState) int {
		L.Push(L.GetGlobal("require"))
		L.Push(LString(MarkupLibName))
		L.Call(1, 1)
		return 1
	})
	return 0
}
