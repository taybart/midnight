package pages

import (
	"context"
	"fmt"
	"net/http"
	"reflect"

	lua "github.com/taybart/midnight"
)

// Funcs makes fns a module of the page, `require(module)`, a table of the
// functions by name. They're plain Go functions, ex.
//
//	func (db *DB) AddItem(ctx context.Context, item Item) (int, error)
//
// called from midnight with `db.add_item({ name = 'apples' })`:
//
//   - A first param of context.Context or *http.Request gets the request
//     being handled (the page load's, or the action's), not an argument.
//   - The other params are converted from the lua args (see convert.go for
//     the types). A missing arg is nil, and too many is an error.
//   - Results can be (), (T), (error), or (T, error). A non-nil error is
//     raised as a lua error (pcall-able), T is converted to lua.
//
// All the reflection on a function's signature happens here: an unsupported
// one panics (naming the module, function, and param), so it fails when the
// handler's built, not when it's called. A call converts its args, calls the
// function (reflect.Value.Call), and converts its result.
//
// lua.LGFunction values (func(*lua.LState) int) are used as is, like in
// LuaFuncs, for full control.
func Funcs(module string, fns map[string]any) Option {
	adapted := make(map[string]lua.LGFunction, len(fns))
	for name, fn := range fns {
		f, err := adapt(module+"."+name, fn)
		if err != nil {
			panic("pages: " + err.Error())
		}
		adapted[name] = f
	}
	return LuaFuncs(module, adapted)
}

var (
	contextType = reflect.TypeFor[context.Context]()
	requestType = reflect.TypeFor[*http.Request]()
	errorType   = reflect.TypeFor[error]()
)

// adapt makes fn a lua function (see Funcs). name is for errors.
func adapt(name string, fn any) (lua.LGFunction, error) {
	switch f := fn.(type) {
	case lua.LGFunction:
		return f, nil
	case func(*lua.LState) int:
		return f, nil
	}
	fv := reflect.ValueOf(fn)
	ft := fv.Type()
	if ft.Kind() != reflect.Func || fv.IsNil() {
		return nil, fmt.Errorf("%s: expected a function, got %s", name, ft)
	}
	if ft.IsVariadic() {
		return nil, fmt.Errorf("%s: variadic functions are not supported", name)
	}

	// params: an injected request / context first, then args
	inject := ft.NumIn() > 0 && (ft.In(0) == contextType || ft.In(0) == requestType)
	first := 0
	if inject {
		first = 1
	}
	args := make([]toGo, ft.NumIn()-first)
	for i := range args {
		c, err := goConverter(ft.In(first + i))
		if err != nil {
			return nil, fmt.Errorf("%s: param %d: %w", name, first+i+1, err)
		}
		args[i] = c
	}

	// results: (), (T), (error), (T, error)
	var result toLua
	errIdx := -1
	switch n := ft.NumOut(); {
	case n > 2:
		return nil, fmt.Errorf("%s: returns %d results, expected at most a value and an error", name, n)
	case n > 0 && ft.Out(n-1) == errorType:
		errIdx = n - 1
		if n == 2 {
			if ft.Out(0) == errorType {
				return nil, fmt.Errorf("%s: returns two errors", name)
			}
			c, err := luaConverter(ft.Out(0))
			if err != nil {
				return nil, fmt.Errorf("%s: result: %w", name, err)
			}
			result = c
		}
	case n == 2:
		return nil, fmt.Errorf("%s: returns (%s, %s), expected (T) or (T, error)", name, ft.Out(0), ft.Out(1))
	case n == 1:
		c, err := luaConverter(ft.Out(0))
		if err != nil {
			return nil, fmt.Errorf("%s: result: %w", name, err)
		}
		result = c
	}

	injectRequest := inject && ft.In(0) == requestType
	return func(l *lua.LState) int {
		if top := l.GetTop(); top > len(args) {
			l.RaiseError("%s: expected %d args, got %d", name, len(args), top)
		}
		in := make([]reflect.Value, ft.NumIn())
		if inject {
			r := Request(l)
			switch {
			case injectRequest:
				in[0] = reflect.ValueOf(r)
			case r != nil:
				in[0] = reflect.ValueOf(r.Context())
			default:
				in[0] = reflect.ValueOf(context.Background())
			}
		}
		for i, conv := range args {
			v, err := conv(l, l.Get(i+1))
			if err != nil {
				l.RaiseError("%s: arg %d%s", name, i+1, argErr(err))
			}
			in[first+i] = v
		}
		out := fv.Call(in)
		if errIdx >= 0 && !out[errIdx].IsNil() {
			l.RaiseError("%s: %s", name, out[errIdx].Interface().(error).Error())
		}
		if result == nil {
			return 0
		}
		v, err := result(l, out[0])
		if err != nil {
			l.RaiseError("%s: result%s", name, argErr(err))
		}
		l.Push(v)
		return 1
	}, nil
}

// argErr formats a conversion error after "arg N" (ex. "[2].name: expected
// string, got nil").
func argErr(err error) string {
	if ce, ok := err.(*convError); ok && ce.path != "" {
		return ce.path + ": " + ce.msg
	}
	return ": " + err.Error()
}
