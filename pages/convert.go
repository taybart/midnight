package pages

import (
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	lua "github.com/taybart/midnight"
	"github.com/taybart/midnight/luaconv"
)

// Converters between lua values and Go types, for Funcs and Props. They're
// built once per type (all the reflection on a type happens then), and
// cached, so converting is just running them.
//
//	Go                          lua
//	string, []byte              string
//	bool                        boolean
//	ints, uints, floats         number (to ints: integers in range only)
//	[]T                         array table (nil slices are {})
//	map[string]T                table (nil maps are {})
//	struct, *struct             table by field name, or its json tag
//	                            (`json:"-"` and unexported fields skipped,
//	                            embedded structs flattened, `omitempty` /
//	                            `omitzero` fields left out (nil) like
//	                            encoding/json, so lua defaults apply)
//	time.Time                   string (RFC 3339)
//	any                         by the value's type (to Go: string, float64,
//	                            bool, []any, map[string]any, or nil)
//
// Pointers are nil or their value. Other types (channels, funcs, complex,
// non-string map keys, interfaces with methods) aren't supported.

// toGo converts a lua value to t (the type it was built for).
type toGo func(l *lua.LState, v lua.LValue) (reflect.Value, error)

// toLua converts a Go value of the type it was built for.
type toLua func(l *lua.LState, v reflect.Value) (lua.LValue, error)

// convError is a conversion error, with the path to where it failed (ex.
// "[2].name"), built on the way back up (so it's only built on failure).
type convError struct {
	path string
	msg  string
}

func (e *convError) Error() string {
	if e.path == "" {
		return e.msg
	}
	return strings.TrimPrefix(e.path, ".") + ": " + e.msg
}

// at prefixes err's path with a segment (ex. "[2]" or ".name").
func at(segment string, err error) error {
	if ce, ok := err.(*convError); ok {
		return &convError{path: segment + ce.path, msg: ce.msg}
	}
	return &convError{path: segment, msg: err.Error()}
}

func expected(what string, got lua.LValue) error {
	return &convError{msg: fmt.Sprintf("expected %s, got %s", what, got.Type())}
}

var (
	timeType = reflect.TypeFor[time.Time]()
)

// The converter caches. A type's converter is built (under convMu) with a
// placeholder in the cache first, so recursive types (ex. a tree) work.
var (
	convMu    sync.Mutex
	goConvs   sync.Map // reflect.Type -> toGo
	luaConvs  sync.Map // reflect.Type -> toLua
	buildGo   = map[reflect.Type]*toGo{}
	buildLua  = map[reflect.Type]*toLua{}
	errNotSup = func(t reflect.Type) error { return fmt.Errorf("%s is not supported", t) }
)

// goConverter returns the converter to t, or an error if t isn't supported.
func goConverter(t reflect.Type) (toGo, error) {
	if c, ok := goConvs.Load(t); ok {
		return c.(toGo), nil
	}
	convMu.Lock()
	defer convMu.Unlock()
	return goConverterLocked(t)
}

func goConverterLocked(t reflect.Type) (toGo, error) {
	if c, ok := goConvs.Load(t); ok {
		return c.(toGo), nil
	}
	if p, ok := buildGo[t]; ok { // recursive type, being built
		return func(l *lua.LState, v lua.LValue) (reflect.Value, error) { return (*p)(l, v) }, nil
	}
	var c toGo
	buildGo[t] = &c
	defer delete(buildGo, t)
	built, err := makeGoConverter(t)
	if err != nil {
		return nil, err
	}
	c = built
	goConvs.Store(t, c)
	return c, nil
}

func makeGoConverter(t reflect.Type) (toGo, error) {
	if t == jsonValueType { // it's only rendered, it can't come back from lua
		return nil, errNotSup(t)
	}
	switch t.Kind() {
	case reflect.String:
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			s, ok := v.(lua.LString)
			if !ok {
				return reflect.Value{}, expected("string", v)
			}
			return reflect.ValueOf(string(s)).Convert(t), nil
		}, nil
	case reflect.Bool:
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			b, ok := v.(lua.LBool)
			if !ok {
				return reflect.Value{}, expected("boolean", v)
			}
			return reflect.ValueOf(bool(b)).Convert(t), nil
		}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			n, ok := v.(lua.LNumber)
			if !ok {
				return reflect.Value{}, expected("integer", v)
			}
			out := reflect.New(t).Elem()
			if f := float64(n); f != math.Trunc(f) || out.OverflowInt(int64(f)) || f > math.MaxInt64 || f < math.MinInt64 {
				return reflect.Value{}, &convError{msg: fmt.Sprintf("%v is not a %s", n, t)}
			}
			out.SetInt(int64(n))
			return out, nil
		}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			n, ok := v.(lua.LNumber)
			if !ok {
				return reflect.Value{}, expected("integer", v)
			}
			out := reflect.New(t).Elem()
			if f := float64(n); f != math.Trunc(f) || f < 0 || f > math.MaxUint64 || out.OverflowUint(uint64(f)) {
				return reflect.Value{}, &convError{msg: fmt.Sprintf("%v is not a %s", n, t)}
			}
			out.SetUint(uint64(n))
			return out, nil
		}, nil
	case reflect.Float32, reflect.Float64:
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			n, ok := v.(lua.LNumber)
			if !ok {
				return reflect.Value{}, expected("number", v)
			}
			return reflect.ValueOf(float64(n)).Convert(t), nil
		}, nil
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 { // []byte
			return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
				s, ok := v.(lua.LString)
				if !ok {
					return reflect.Value{}, expected("string", v)
				}
				return reflect.ValueOf([]byte(s)).Convert(t), nil
			}, nil
		}
		elem, err := goConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v lua.LValue) (reflect.Value, error) {
			if v == lua.LNil {
				return reflect.Zero(t), nil
			}
			tbl, ok := v.(*lua.LTable)
			if !ok {
				return reflect.Value{}, expected("table", v)
			}
			n := tbl.Len()
			out := reflect.MakeSlice(t, n, n)
			for i := range n {
				e, err := elem(l, tbl.RawGetInt(i+1))
				if err != nil {
					return reflect.Value{}, at("["+strconv.Itoa(i+1)+"]", err)
				}
				out.Index(i).Set(e)
			}
			return out, nil
		}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, errNotSup(t)
		}
		elem, err := goConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v lua.LValue) (reflect.Value, error) {
			if v == lua.LNil {
				return reflect.Zero(t), nil
			}
			tbl, ok := v.(*lua.LTable)
			if !ok {
				return reflect.Value{}, expected("table", v)
			}
			out := reflect.MakeMap(t)
			var convErr error
			tbl.ForEach(func(k, ev lua.LValue) {
				if convErr != nil {
					return
				}
				key, ok := k.(lua.LString)
				if !ok {
					convErr = &convError{msg: fmt.Sprintf("expected string keys, got a %s key", k.Type())}
					return
				}
				e, err := elem(l, ev)
				if err != nil {
					convErr = at("."+string(key), err)
					return
				}
				out.SetMapIndex(reflect.ValueOf(string(key)).Convert(t.Key()), e)
			})
			return out, convErr
		}, nil
	case reflect.Struct:
		if t == timeType {
			return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
				s, ok := v.(lua.LString)
				if !ok {
					return reflect.Value{}, expected("time (an RFC 3339 string)", v)
				}
				tm, err := time.Parse(time.RFC3339Nano, string(s))
				if err != nil {
					return reflect.Value{}, &convError{msg: err.Error()}
				}
				return reflect.ValueOf(tm), nil
			}, nil
		}
		fields, err := structFields(t)
		if err != nil {
			return nil, err
		}
		convs := make([]toGo, len(fields))
		for i, f := range fields {
			if convs[i], err = goConverterLocked(f.typ); err != nil {
				return nil, fmt.Errorf("field %s: %w", f.name, err)
			}
		}
		return func(l *lua.LState, v lua.LValue) (reflect.Value, error) {
			tbl, ok := v.(*lua.LTable)
			if !ok {
				return reflect.Value{}, expected("table", v)
			}
			out := reflect.New(t).Elem()
			for i, f := range fields {
				fv := tbl.RawGetString(f.name)
				if fv == lua.LNil {
					continue // zero value
				}
				e, err := convs[i](l, fv)
				if err != nil {
					return reflect.Value{}, at("."+f.name, err)
				}
				out.FieldByIndex(f.index).Set(e)
			}
			return out, nil
		}, nil
	case reflect.Pointer:
		elem, err := goConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v lua.LValue) (reflect.Value, error) {
			if v == lua.LNil {
				return reflect.Zero(t), nil
			}
			e, err := elem(l, v)
			if err != nil {
				return reflect.Value{}, err
			}
			p := reflect.New(t.Elem())
			p.Elem().Set(e)
			return p, nil
		}, nil
	case reflect.Interface:
		if t.NumMethod() != 0 {
			return nil, errNotSup(t)
		}
		return func(_ *lua.LState, v lua.LValue) (reflect.Value, error) {
			if v == lua.LNil {
				return reflect.Zero(t), nil
			}
			return reflect.ValueOf(luaconv.ToGo(v)), nil
		}, nil
	}
	return nil, errNotSup(t)
}

// luaConverter returns the converter from t, or an error if t isn't
// supported.
func luaConverter(t reflect.Type) (toLua, error) {
	if c, ok := luaConvs.Load(t); ok {
		return c.(toLua), nil
	}
	convMu.Lock()
	defer convMu.Unlock()
	return luaConverterLocked(t)
}

func luaConverterLocked(t reflect.Type) (toLua, error) {
	if c, ok := luaConvs.Load(t); ok {
		return c.(toLua), nil
	}
	if p, ok := buildLua[t]; ok { // recursive type, being built
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) { return (*p)(l, v) }, nil
	}
	var c toLua
	buildLua[t] = &c
	defer delete(buildLua, t)
	built, err := makeLuaConverter(t)
	if err != nil {
		return nil, err
	}
	c = built
	luaConvs.Store(t, c)
	return c, nil
}

func makeLuaConverter(t reflect.Type) (toLua, error) {
	if t == jsonValueType {
		return jsonToLua, nil
	}
	switch t.Kind() {
	case reflect.String:
		return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LString(v.String()), nil }, nil
	case reflect.Bool:
		return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LBool(v.Bool()), nil }, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LNumber(v.Int()), nil }, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LNumber(v.Uint()), nil }, nil
	case reflect.Float32, reflect.Float64:
		return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LNumber(v.Float()), nil }, nil
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 && t.Kind() == reflect.Slice { // []byte
			return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) { return lua.LString(v.Bytes()), nil }, nil
		}
		elem, err := luaConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) {
			n := v.Len()
			tbl := l.CreateTable(n, 0)
			for i := range n {
				e, err := elem(l, v.Index(i))
				if err != nil {
					return nil, at("["+strconv.Itoa(i+1)+"]", err)
				}
				tbl.RawSetInt(i+1, e)
			}
			return tbl, nil
		}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, errNotSup(t)
		}
		elem, err := luaConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) {
			tbl := l.CreateTable(0, v.Len())
			iter := v.MapRange()
			for iter.Next() {
				key := iter.Key().String()
				e, err := elem(l, iter.Value())
				if err != nil {
					return nil, at("."+key, err)
				}
				tbl.RawSetString(key, e)
			}
			return tbl, nil
		}, nil
	case reflect.Struct:
		if t == timeType {
			return func(_ *lua.LState, v reflect.Value) (lua.LValue, error) {
				return lua.LString(v.Interface().(time.Time).Format(time.RFC3339Nano)), nil
			}, nil
		}
		fields, err := structFields(t)
		if err != nil {
			return nil, err
		}
		convs := make([]toLua, len(fields))
		for i, f := range fields {
			if convs[i], err = luaConverterLocked(f.typ); err != nil {
				return nil, fmt.Errorf("field %s: %w", f.name, err)
			}
		}
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) {
			tbl := l.CreateTable(0, len(fields))
			for i, f := range fields {
				fv := v.FieldByIndex(f.index)
				if f.omit != nil && f.omit(fv) {
					continue
				}
				e, err := convs[i](l, fv)
				if err != nil {
					return nil, at("."+f.name, err)
				}
				tbl.RawSetString(f.name, e)
			}
			return tbl, nil
		}, nil
	case reflect.Pointer:
		elem, err := luaConverterLocked(t.Elem())
		if err != nil {
			return nil, err
		}
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) {
			if v.IsNil() {
				return lua.LNil, nil
			}
			return elem(l, v.Elem())
		}, nil
	case reflect.Interface:
		return func(l *lua.LState, v reflect.Value) (lua.LValue, error) {
			if v.IsNil() {
				return lua.LNil, nil
			}
			return toLuaValue(l, v.Elem().Interface())
		}, nil
	}
	return nil, errNotSup(t)
}

// toLuaValue converts any supported Go value (by its type, see the table at
// the top) to lua.
func toLuaValue(l *lua.LState, v any) (lua.LValue, error) {
	switch v := v.(type) { // common ones, without reflection
	case nil:
		return lua.LNil, nil
	case string:
		return lua.LString(v), nil
	case bool:
		return lua.LBool(v), nil
	case int:
		return lua.LNumber(v), nil
	case float64:
		return lua.LNumber(v), nil
	case lua.LValue:
		return v, nil
	case JSONValue:
		return jsonToLua(l, reflect.ValueOf(v))
	}
	c, err := luaConverter(reflect.TypeOf(v))
	if err != nil {
		return nil, err
	}
	return c(l, reflect.ValueOf(v))
}

type structField struct {
	name  string
	index []int
	typ   reflect.Type
	omit  func(reflect.Value) bool // `omitempty` / `omitzero`: whether to leave it out
}

// structFields returns t's fields like encoding/json names them: by their
// json tag, or name, without `json:"-"` or unexported ones, with embedded structs'
// fields flattened (shallower fields win, the first of the same depth).
func structFields(t reflect.Type) ([]structField, error) {
	var fields []structField
	seen := map[string]int{} // name -> depth
	var walk func(t reflect.Type, index []int)
	walk = func(t reflect.Type, index []int) {
		for i := range t.NumField() {
			f := t.Field(i)
			idx := append(append([]int{}, index...), i)
			tag := f.Tag.Get("json")
			if tag == "-" {
				continue
			}
			name, opts, _ := strings.Cut(tag, ",")
			// (embedded pointers aren't flattened: setting their fields would
			// need allocating them)
			if f.Anonymous && name == "" && f.Type.Kind() == reflect.Struct && f.Type != timeType {
				walk(f.Type, idx)
				continue
			}
			if !f.IsExported() {
				continue
			}
			if name == "" {
				name = f.Name
			}
			if depth, ok := seen[name]; ok && depth <= len(idx) {
				continue
			}
			seen[name] = len(idx)
			fields = append(fields, structField{name: name, index: idx, typ: f.Type, omit: omitFunc(f.Type, opts)})
		}
	}
	walk(t, nil)
	// a shallower field may have replaced a deeper one with the same name
	out := fields[:0]
	for _, f := range fields {
		if seen[f.name] == len(f.index) {
			out = append(out, f)
		}
	}
	return out, nil
}

var isZeroerType = reflect.TypeFor[interface{ IsZero() bool }]()

// omitFunc returns whether to leave out a field with these json tag options,
// like encoding/json: `omitempty` (false, 0, "", nil, and empty slices / maps
// / arrays) and `omitzero` (the zero value, or IsZero() if it has one). nil if
// neither.
func omitFunc(t reflect.Type, opts string) func(reflect.Value) bool {
	var empty, zero bool
	for opts != "" {
		var opt string
		opt, opts, _ = strings.Cut(opts, ",")
		empty = empty || opt == "omitempty"
		zero = zero || opt == "omitzero"
	}
	var isZero func(reflect.Value) bool
	if zero {
		isZero = reflect.Value.IsZero
		if t.Implements(isZeroerType) {
			isZero = func(v reflect.Value) bool {
				if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
					return true
				}
				return v.Interface().(interface{ IsZero() bool }).IsZero()
			}
		} else if reflect.PointerTo(t).Implements(isZeroerType) {
			isZero = func(v reflect.Value) bool {
				if !v.CanAddr() { // (a copy, so it can call the method)
					p := reflect.New(t)
					p.Elem().Set(v)
					v = p.Elem()
				}
				return v.Addr().Interface().(interface{ IsZero() bool }).IsZero()
			}
		}
	}
	switch {
	case empty && zero:
		return func(v reflect.Value) bool { return isEmpty(v) || isZero(v) }
	case empty:
		return isEmpty
	case zero:
		return isZero
	}
	return nil
}

// isEmpty is encoding/json's empty value, for `omitempty`.
func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}
