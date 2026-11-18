package pages

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	lua "github.com/taybart/midnight"
)

type Base struct {
	ID int `json:"id"`
}

type Item struct {
	Base              // flattened: id
	Name     string   `json:"name"`
	Tags     []string `json:"tags,omitempty"`
	Qty      uint8    `json:"qty"`
	Meta     map[string]any
	When     time.Time `json:"when"`
	Next     *Item     `json:"next"` // recursive
	Secret   string    `json:"-"`
	internal string
}

// run runs lua code with fns as globals, returning its results as strings.
func run(t *testing.T, fns map[string]any, code string) ([]string, error) {
	t.Helper()
	return runLang(t, fns, code, false)
}

func runLang(t *testing.T, fns map[string]any, code string, midnight bool) ([]string, error) {
	t.Helper()
	l := lua.NewState()
	defer l.Close()
	for name, fn := range fns {
		f, err := adapt(name, fn)
		if err != nil {
			t.Fatal(err)
		}
		l.SetGlobal(name, l.NewFunction(f))
	}
	l.SetTop(0)
	load := l.LoadString
	if midnight {
		load = l.LoadMidnightString
	}
	fn, err := load(code)
	if err != nil {
		return nil, err
	}
	l.Push(fn)
	if err := l.PCall(0, lua.MultRet, nil); err != nil {
		return nil, err
	}
	var out []string
	for i := 1; i <= l.GetTop(); i++ {
		out = append(out, l.Get(i).String())
	}
	return out, nil
}

// Unsupported signatures are errors when adapting (Funcs panics with them).
func TestAdaptErrors(t *testing.T) {
	for _, c := range []struct {
		fn   any
		want string
	}{
		{42, "expected a function, got int"},
		{func(chan int) {}, "f: param 1: chan int is not supported"},
		{func(context.Context, string, func()) {}, "f: param 3: func() is not supported"},
		{func(map[int]string) {}, "param 1: map[int]string is not supported"},
		{func(error) {}, "param 1: error is not supported"},
		{func(...string) {}, "variadic functions are not supported"},
		{func() (int, string, error) { return 0, "", nil }, "returns 3 results"},
		{func() (int, string) { return 0, "" }, "returns (int, string), expected (T) or (T, error)"},
		{func() (error, error) { return nil, nil }, "returns two errors"},
		{func() chan int { return nil }, "f: result: chan int is not supported"},
		{func() struct{ C chan int } { return struct{ C chan int }{} }, "result: field C: chan int is not supported"},
	} {
		_, err := adapt("f", c.fn)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%T: got %v, want %q", c.fn, err, c.want)
		}
	}
	defer func() {
		if r := recover(); r == nil || !strings.Contains(r.(string), "pages: app.db.bad: param 1") {
			t.Errorf("Funcs: expected a panic, got %v", r)
		}
	}()
	Funcs("app.db", map[string]any{"bad": func(chan int) {}})
}

// Values convert both ways: structs (by json name, flattened, recursive),
// slices, maps, times, pointers, any.
func TestFuncsConvert(t *testing.T) {
	when := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	echo := func(it Item) (Item, error) {
		it.Name += "!"
		it.Secret = "hidden"
		it.internal = "hidden"
		return it, nil
	}
	out, err := run(t, map[string]any{"echo": echo}, `
local it = echo({
  id = 7, name = 'apples', tags = { 'a', 'b' }, qty = 3,
  Meta = { n = 1, list = { 1, 2 } },
  when = '2026-11-07T12:00:00Z',
  next = { name = 'pears' },
  Secret = 'ignored',
})
return it.id, it.name, table.concat(it.tags, ','), it.qty, it.Meta.n, it.Meta.list[2],
  it.when, it.next.name, tostring(it.next.next), tostring(it.Secret), tostring(it.internal),
  tostring(it.Base), tostring(echo({}).tags)`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"7", "apples!", "a,b", "3", "1", "2", when.Format(time.RFC3339Nano), "pears", "nil", "nil", "nil", "nil", "nil"}
	if strings.Join(out, "|") != strings.Join(want, "|") {
		t.Errorf("got  %v\nwant %v", out, want)
	}
}

type zeroer struct{ n int }

func (z zeroer) IsZero() bool { return z.n < 0 } // (only negatives are "zero")

type ptrZeroer struct{ n int }

func (z *ptrZeroer) IsZero() bool { return z.n == 7 }

type Omit struct {
	S     string          `json:"s,omitempty"`
	N     int             `json:"n,omitempty"`
	B     bool            `json:"b,omitempty"`
	P     *int            `json:"p,omitempty"`
	L     []int           `json:"l,omitempty"`
	M     map[string]int  `json:"m,omitempty"`
	When  time.Time       `json:"when,omitzero"`
	Inner struct{ A int } `json:"inner,omitzero"`
	Z     zeroer          `json:"z,omitzero"`
	PZ    ptrZeroer       `json:"pz,omitzero"`
	Both  string          `json:"both,omitempty,omitzero"`
	Kept  string          `json:"kept"`
}

// omitempty / omitzero fields are left out (nil), so lua defaults apply.
func TestFuncsOmit(t *testing.T) {
	one := 1
	empty := func() Omit { return Omit{Z: zeroer{-1}, PZ: ptrZeroer{7}} }
	full := func() Omit {
		return Omit{S: "s", N: 1, B: true, P: &one, L: []int{1}, M: map[string]int{"a": 1},
			When: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), Inner: struct{ A int }{1}, Z: zeroer{0}, PZ: ptrZeroer{1}, Both: "x"}
	}
	out, err := runLang(t, map[string]any{"empty": empty, "full": full}, `
local names = { 's', 'n', 'b', 'p', 'l', 'm', 'when', 'inner', 'z', 'pz', 'both', 'kept' }
local function present(t) {
  local out = {}
  for _, k in ipairs(names) { if t[k] != nil { out[#out + 1] = k } }
  return table.concat(out, ',')
}
local { s = 'default', kept = 'no' } = empty()
return present(empty()), present(full()), s, kept`, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kept", "s,n,b,p,l,m,when,inner,z,pz,both,kept", "default", ""}
	if strings.Join(out, "|") != strings.Join(want, "|") {
		t.Errorf("got  %v\nwant %v", out, want)
	}
}

// Bad args are lua errors (pcall-able), with the path to what's wrong.
func TestFuncsArgErrors(t *testing.T) {
	fns := map[string]any{
		"items": func([]Item) {},
		"small": func(int8) {},
		"name":  func(string) {},
		"fails": func() error { return errors.New("db is down") },
		"byKey": func(map[string]int) {},
	}
	for code, want := range map[string]string{
		`items({ { name = 'a' }, { name = 2 } })`: "items: arg 1[2].name: expected string, got number",
		`small(1.5)`:                "small: arg 1: 1.5 is not a int8",
		`small(300)`:                "small: arg 1: 300 is not a int8",
		`name()`:                    "name: arg 1: expected string, got nil",
		`name('a', 'b')`:            "name: expected 1 args, got 2",
		`fails()`:                   "fails: db is down",
		`byKey({ a = 1, b = 'x' })`: "byKey: arg 1.b: expected integer, got string",
		`byKey({ 1 })`:              "byKey: arg 1: expected string keys, got a number key",
	} {
		out, err := run(t, fns, `local ok, err = pcall(function() `+code+` end) return tostring(ok), err`)
		if err != nil {
			t.Fatal(err)
		}
		if out[0] != "false" || !strings.Contains(out[1], want) {
			t.Errorf("%s: got %v, want %q", code, out, want)
		}
	}
}

// A first context.Context / *http.Request param gets the request being
// handled.
func TestFuncsInject(t *testing.T) {
	type key struct{}
	l := lua.NewState()
	defer l.Close()
	user, err := adapt("user", func(r *http.Request, prefix string) string { return prefix + r.Header.Get("X-User") })
	if err != nil {
		t.Fatal(err)
	}
	fromCtx, err := adapt("fromCtx", func(ctx context.Context) any { return ctx.Value(key{}) })
	if err != nil {
		t.Fatal(err)
	}
	l.SetGlobal("user", l.NewFunction(user))
	l.SetGlobal("fromCtx", l.NewFunction(fromCtx))

	if err := l.DoString(`assert(fromCtx() == nil)`); err != nil { // no request: context.Background()
		t.Fatal(err)
	}
	r, _ := http.NewRequestWithContext(context.WithValue(context.Background(), key{}, "v"), "GET", "/", nil)
	r.Header.Set("X-User", "alice")
	setRequest(l, r)
	if err := l.DoString(`assert(user('hi ') == 'hi alice'); assert(fromCtx() == 'v')`); err != nil {
		t.Fatal(err)
	}
}

// Calls: LuaFuncs (a plain lua.LGFunction) vs Funcs (reflection), per call.
func BenchmarkCall(b *testing.B) {
	type Out struct {
		Name string `json:"name"`
		N    int    `json:"n"`
	}
	for _, c := range []struct {
		name string
		fn   any
		args string
	}{
		{"LuaFuncs/add", lua.LGFunction(func(l *lua.LState) int {
			l.Push(l.CheckNumber(1) + l.CheckNumber(2))
			return 1
		}), "i, 2"},
		{"Funcs/add", func(a, b int) int { return a + b }, "i, 2"},
		{"LuaFuncs/ctx_string_error", lua.LGFunction(func(l *lua.LState) int {
			_ = Request(l)
			l.Push(lua.LString(l.CheckString(1) + "!"))
			return 1
		}), "'x'"},
		{"Funcs/ctx_string_error", func(_ context.Context, s string) (string, error) { return s + "!", nil }, "'x'"},
		{"Funcs/struct_result", func(n int) Out { return Out{Name: "x", N: n} }, "i"},
	} {
		b.Run(c.name, func(b *testing.B) {
			l := lua.NewState()
			defer l.Close()
			f, err := adapt("f", c.fn)
			if err != nil {
				b.Fatal(err)
			}
			l.SetGlobal("f", l.NewFunction(f))
			loop, err := l.LoadString(`local f, n = f, ... for i = 1, n do f(` + c.args + `) end`)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			if err := l.CallByParam(lua.P{Fn: loop, Protect: true}, lua.LNumber(b.N)); err != nil {
				b.Fatal(err)
			}
		})
	}
}
