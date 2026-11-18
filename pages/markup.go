package pages

import (
	lua "github.com/taybart/midnight"
)

// Pages are midnight scripts that return a markup element (ex.
// `return <App/>`), served as interactive pages. Functions in
// `on*` attributes of its elements become actions: clicking (etc.) POSTs to
// the page's rpc url, which runs the function and responds with the components
// that changed (see midnight.markup's mount / dispatch).
//
// A page's lua state is kept for its actions (see StateStore): the page sends
// its id back in the StateHeader.

// mountScript mounts the element a page script returned (args: element, rpc
// url or nil, state id), so rendering it includes the client side of actions.
const mountScript = `
local root, rpc, state = ...
return require('midnight.markup').mount(function() return root end, {
  rpc = rpc,
  headers = { ['` + StateHeader + `'] = state },
})
`

// dispatch runs the action with this id (in the page's markup runtime),
// returning its response table.
func dispatch(l *lua.LState, id string, data lua.LValue) (lua.LValue, error) {
	if err := l.CallByParam(lua.P{Fn: l.GetGlobal("require"), NRet: 1, Protect: true}, lua.LString(lua.MarkupLibName)); err != nil {
		return nil, err
	}
	markup := l.Get(-1)
	l.Pop(1)
	if err := l.CallByParam(lua.P{Fn: l.GetField(markup, "dispatch"), NRet: 1, Protect: true}, lua.LString(id), data); err != nil {
		return nil, err
	}
	res := l.Get(-1)
	l.Pop(1)
	return res, nil
}

// MarkupElement returns the markup runtime if v is a markup element (built by
// midnight.markup, ex. `<App/>` in a midnight script).
func MarkupElement(l *lua.LState, v lua.LValue) (lua.LValue, bool, error) {
	// only tables with a metatable can be elements, so plain responses don't
	// load the runtime
	if _, ok := v.(*lua.LTable); !ok || l.GetMetatable(v) == lua.LNil {
		return nil, false, nil
	}
	if err := l.CallByParam(lua.P{Fn: l.GetGlobal("require"), NRet: 1, Protect: true}, lua.LString(lua.MarkupLibName)); err != nil {
		return nil, false, err
	}
	markup := l.Get(-1)
	l.Pop(1)

	if err := l.CallByParam(lua.P{Fn: l.GetField(markup, "is_element"), NRet: 1, Protect: true}, v); err != nil {
		return nil, false, err
	}
	isElement := lua.LVAsBool(l.Get(-1))
	l.Pop(1)
	return markup, isElement, nil
}

// RenderDocument renders v as an HTML document if it's a markup element.
func RenderDocument(l *lua.LState, v lua.LValue) (string, bool, error) {
	markup, isElement, err := MarkupElement(l, v)
	if err != nil || !isElement {
		return "", false, err
	}
	if err := l.CallByParam(lua.P{Fn: l.GetField(markup, "document"), NRet: 1, Protect: true}, v); err != nil {
		return "", false, err
	}
	html := l.Get(-1).String()
	l.Pop(1)
	return html, true, nil
}

// Mount mounts what a page script returned, if it's an element, so it renders
// with the client side of its actions, which POST to rpc with the state id. With an
// empty rpc, they POST to the page's own url (as the browser sees it). A page
// may also return a response table (ex. a redirect), which is returned as is.
func Mount(l *lua.LState, v lua.LValue, rpc, stateID string) (lua.LValue, error) {
	if _, isElement, err := MarkupElement(l, v); err != nil || !isElement {
		return v, err
	}
	fn, err := l.LoadString(mountScript)
	if err != nil {
		return nil, err
	}
	var rpcURL lua.LValue = lua.LNil
	if rpc != "" {
		rpcURL = lua.LString(rpc)
	}
	if err := l.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}, v, rpcURL, lua.LString(stateID)); err != nil {
		return nil, err
	}
	mounted := l.Get(-1)
	l.Pop(1)
	return mounted, nil
}

// IsInteractive returns whether the page that was just rendered has actions.
// Pages without any never call back, so their state doesn't need keeping.
func IsInteractive(l *lua.LState) (bool, error) {
	if l.GetField(l.GetField(l.GetGlobal("package"), "loaded"), lua.MarkupLibName) == lua.LNil {
		return false, nil // not a markup page (ex. a redirect)
	}
	fn, err := l.LoadString(`return require('midnight.markup').is_interactive()`)
	if err != nil {
		return false, err
	}
	if err := l.CallByParam(lua.P{Fn: fn, NRet: 1, Protect: true}); err != nil {
		return false, err
	}
	interactive := lua.LVAsBool(l.Get(-1))
	l.Pop(1)
	return interactive, nil
}
