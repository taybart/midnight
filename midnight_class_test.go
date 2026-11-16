package lua

import (
	"strings"
	"testing"

	"github.com/taybart/midnight/luagen"
	"github.com/taybart/midnight/parse"
)

const midnightClassAnimal = `
local class Animal {
  legs = 4
  new(name, sound = '...') {
    self.name = name
    self.sound = sound
  }
  speak() { return "{self.name} says {self.sound}" }
  __tostring() { return "<{self.name}>" }
}
`

var midnightClassCases = []struct {
	source string
	want   string
}{
	// constructor, methods, defaults, fields, metamethods
	{midnightClassAnimal + `return Animal.new('cat', 'meow'):speak()`, "cat says meow"},
	{midnightClassAnimal + `return Animal.new('rock'):speak()`, "rock says ..."},
	{midnightClassAnimal + `return Animal.new('cat').legs`, "4"},
	{midnightClassAnimal + `return tostring(Animal.new('cat'))`, "<cat>"},
	{midnightClassAnimal + `return getmetatable(Animal.new('cat')) == Animal`, "true"},
	// inheritance + super
	{midnightClassAnimal + `
local class Bird : Animal {
  legs = 2
  new(name) {
    self.name = name
    self.sound = 'tweet'
  }
  speak() { return super.speak(self) .. '!' }
}
local b = Bird.new('robin')
return "{b:speak()} {b.legs} {tostring(b)}"`, "robin says tweet! 2 <robin>"},
	// super is resolved at the class header, not shadowed by member locals
	{`local class A { f() { return 'a' } }
local class B : A { f() { local A = nil; return super.f(self) .. 'b' } }
return B.new():f()`, "ab"},
	// no constructor: inherited `new` builds a parent instance; own `new` is implicit-free
	{`local class A { new() { self.x = 1 } }
local class B : A {}
return B.new().x`, "1"},
	// empty constructor still returns self; explicit return overrides
	{`local class A { new() {} id() { return 7 } }; return A.new():id()`, "7"},
	{`local class A { new() { return 'custom' } }; return A.new()`, "custom"},
	// varargs, destructured params
	{`local class A { sum(...) { local s = 0; for _, v in ipairs({...}) { s += v }; return s } }
return A.new():sum(1, 2, 3)`, "6"},
	{`local class A { get({ x, y = 2 }) { return x + y } }; return A.new():get({ x = 1 })`, "3"},
	// if-expression field values
	{`local debug = true; local class A { level = if debug { 'debug' } else { 'info' } }; return A.level`, "debug"},
	// members separated by commas / semicolons, on one line
	{`local class A { a = 1, b = 2; c() { return self.a + self.b } }; return A.new():c()`, "3"},
	// methods can refer to the class by name, and to outer locals
	{`local n = 10; local class A { new() { self.v = n } clone() { return A.new() } }; return A.new():clone().v`, "10"},
	// scopes: module, bare (global)
	{`module class A { new() { self.v = 1 } }`, ""},
	{`class G { v() { return 'g' } }; return G.new():v()`, "g"},
	// a nested class inside a method body has its own (absent) super
	{`local class P { tag() { return 'p' } }
local class C : P { make() { local class Inner { tag() { return super } }; return Inner.new():tag() } }
return C.new():make() == nil`, "true"},
	// static functions: no self, called w/ `.`
	{`local class A { static double(x) { return x * 2 } }; return A.double(4)`, "8"},
	{`local class A { new(v) { self.v = v } static of(v) { return A.new(v) } }; return A.of(3).v`, "3"},
	{`local self = 'outer'; local class A { static f() { return self } }; return A.f()`, "outer"},
	// a static `new` builds the instance itself (no generated constructor)
	{`local class A { static new(v) { return setmetatable({ v = v * 10 }, A) } get() { return self.v } }; return A.new(2):get()`, "20"},
	// static functions are inherited
	{`local class A { static make() { return 'made' } }; local class B : A {}; return B.make()`, "made"},
	// `static` is still a name for fields and methods
	{`local class A { static = 5 }; return A.static`, "5"},
	{`local class A { static() { return 'm' } }; return A.new():static()`, "m"},
	// `class` is still a name everywhere else
	{`local class = 'x'; return class`, "x"},
	{`local t = { class = 'y' }; return t.class`, "y"},
	{`local class
class = 'z'
return class`, "z"},
	{`local function class(x) { return x } return class('w')`, "w"},
	// `super` is a normal name outside of a subclass
	{`local super = 's'; local class A { f() { return super } }; return A.new():f()`, "s"},
}

func TestMidnightClass(t *testing.T) {
	for _, c := range midnightClassCases {
		L := NewState()
		if err := L.DoMidnightString(c.source); err != nil {
			t.Errorf("%s\n%v", c.source, err)
		} else if c.want != "" {
			if got := L.Get(-1).String(); got != c.want {
				t.Errorf("%s\n  got %s, want %s", c.source, got, c.want)
			}
		}
		L.Close()
	}
}

func TestLuagenMidnightClass(t *testing.T) {
	for _, c := range midnightClassCases {
		chunk, err := parse.ParseMidnight(strings.NewReader(c.source), "<string>")
		if err != nil {
			t.Fatalf("%s\n%v", c.source, err)
		}
		generated := luagen.Generate(chunk)

		L := NewState()
		if err := L.DoString(generated); err != nil {
			t.Errorf("%s\n%s\n%v", c.source, generated, err)
		} else if c.want != "" {
			if got := L.Get(-1).String(); got != c.want {
				t.Errorf("%s\n%s\n  got %s, want %s", c.source, generated, got, c.want)
			}
		}
		L.Close()
	}
}

func TestMidnightModuleClass(t *testing.T) {
	L := NewState()
	defer L.Close()
	if err := L.DoMidnightString(`module class A { new(v) { self.v = v } get() { return self.v } }`); err != nil {
		t.Fatal(err)
	}
	L.SetGlobal("m", L.Get(-1))
	if err := L.DoString(`return m.A.new(5):get()`); err != nil {
		t.Fatal(err)
	}
	if got := L.Get(-1).String(); got != "5" {
		t.Errorf("got %s, want 5", got)
	}
}

func TestMidnightClassSyntaxErrors(t *testing.T) {
	cases := []struct {
		source string
		want   string
	}{
		{"local class A {", "at EOF"},
		{"local class A {\n  f() {", "at EOF"},
		{`local class A { f }`, "expected '(' or '='"},
		{`local class A { 1 }`, "unexpected token '1'"},
		{`local class A : { }`, "unexpected token '{'"},
		{`do { module class A {} }`, "module declarations must appear at the top level"},
		{`local class A { static x = 1 }`, "fields are already static"},
		{`local class A { static f }`, "expected '('"},
	}

	for _, c := range cases {
		L := NewState()
		_, err := L.LoadMidnightString(c.source)
		L.Close()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s\n  got %v, want error containing %q", c.source, err, c.want)
		}
	}
}
