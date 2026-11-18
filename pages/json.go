package pages

import (
	"encoding/json"
	"fmt"
	"reflect"

	lua "github.com/taybart/midnight"
)

// JSONValue is data for a page's own js (see JSON).
type JSONValue struct {
	text string
	err  error
}

// JSON encodes v with encoding/json, for a page to hand to its js in a JSON
// <script> (`<Data id="x" value={props.x}/>`, or `<script
// type="application/json">{props.x}</script>`):
//
//	pages.Props(func(r *http.Request) (any, error) {
//	    return map[string]any{"repos": pages.JSON(repos)}, nil
//	})
//
// In a page it's opaque (it can be rendered, not read): the data goes from
// Go to the browser as is, without becoming lua tables, so its types are Go's
// (an empty map is {}, not [] as a lua table would be), and big data costs
// the page nothing. encoding/json escapes <, >, and &, so nothing in the
// data can close the <script>.
//
// An encoding error (ex. a channel in v) is the page's error (a 500).
func JSON(v any) JSONValue {
	b, err := json.Marshal(v)
	return JSONValue{text: string(b), err: err}
}

var jsonValueType = reflect.TypeFor[JSONValue]()

// jsonToLua makes a JSONValue markup's encoded JSON (json_text).
func jsonToLua(l *lua.LState, v reflect.Value) (lua.LValue, error) {
	j := v.Interface().(JSONValue)
	if j.err != nil {
		return nil, fmt.Errorf("pages.JSON: %w", j.err)
	}
	if err := l.CallByParam(lua.P{Fn: l.GetGlobal("require"), NRet: 1, Protect: true}, lua.LString(lua.MarkupLibName)); err != nil {
		return nil, err
	}
	markup := l.Get(-1)
	l.Pop(1)
	if err := l.CallByParam(lua.P{Fn: l.GetField(markup, "json_text"), NRet: 1, Protect: true}, lua.LString(j.text)); err != nil {
		return nil, err
	}
	res := l.Get(-1)
	l.Pop(1)
	return res, nil
}
