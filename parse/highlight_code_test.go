package parse

import (
	"strings"
	"testing"
)

func codeString(text string, spans []CodeSpan) string {
	var b strings.Builder
	end := 0
	for _, s := range spans {
		if s.Start != end {
			b.WriteString("<gap>")
		}
		end = s.End
		if s.Class == ClassPlain {
			b.WriteString(text[s.Start:s.End])
			continue
		}
		b.WriteString("[" + s.Class + ":" + text[s.Start:s.End] + "]")
	}
	if end != len(text) {
		b.WriteString("<short>")
	}
	return b.String()
}

func TestHighlightCode(t *testing.T) {
	tests := []struct{ lang, src, want string }{
		{"midnight", `local x = f(1) -- hi`,
			`[keyword:local] x [operator:=] [function:f][punct:(][number:1][punct:)] [comment:-- hi]`},
		{"mn", `print(t.k, "a {b}")`,
			`[builtin:print][punct:(]t[punct:.]k[punct:,] [string:"a ][interp:{]b[interp:}][string:"][punct:)]`},
		{"midnight", `<a href={u}>hi</a>`,
			`[punct:<][tag:a] [attr:href][punct:=][punct:{]u[punct:}][punct:>]hi[punct:</][tag:a][punct:>]`},
		// contextual keywords
		{"midnight", `match x { _ => {} }`,
			`[keyword:match] x [punct:{] _ [operator:=>] [punct:{][punct:}] [punct:}]`},
		{"midnight", `s:match(p)`, `s[punct::][function:match][punct:(]p[punct:)]`},
		{"midnight", `f() catch e {}`, `[function:f][punct:(][punct:)] [keyword:catch] e [punct:{][punct:}]`},
		{"midnight", `f() catch {}`, `[function:f][punct:(][punct:)] [keyword:catch] [punct:{][punct:}]`},
		{"midnight", `catch(x)`, `[function:catch][punct:(]x[punct:)]`},
		{"midnight", "local class A : B { static of() {} }",
			`[keyword:local] [keyword:class] A [punct::] B [punct:{] [keyword:static] [function:of][punct:(][punct:)] [punct:{][punct:}] [punct:}]`},
		{"midnight", "local class = 1\nclass\nfoo()",
			"[keyword:local] class [operator:=] [number:1]\nclass\n[function:foo][punct:(][punct:)]"},
		{"midnight", "const x = 1", `[keyword:const] x [operator:=] [number:1]`},
		{"midnight", "module const { a } = t", `[keyword:module] [keyword:const] [punct:{] a [punct:}] [operator:=] t`},
		{"midnight", "const = 1", `const [operator:=] [number:1]`},
		// lua
		{"lua", `local function f(a) return a ~= nil and #a end -- c`,
			`[keyword:local] [keyword:function] [function:f][punct:(]a[punct:)] [keyword:return] a [operator:~=] [const:nil] [keyword:and] [operator:#]a [keyword:end] [comment:-- c]`},
		{"lua", `x = [==[a]]b]==] .. 'q\'' .. 0x1F .. 1e-3`,
			`x [operator:=] [string:[==[a]]b]==]] [operator:..] [string:'q\'']` + ` [operator:..] [number:0x1F] [operator:..] [number:1e-3]`},
		{"lua", "--[[ long\ncomment ]] t:m(...)",
			"[comment:--[[ long\ncomment ]]] t[punct::][function:m][punct:(][operator:...][punct:)]"},
		{"lua", `s = "unterminated`, `s [operator:=] [string:"unterminated]`},
	}
	for _, tt := range tests {
		spans, ok := HighlightCode(tt.lang, tt.src)
		if !ok {
			t.Fatalf("%s: unknown language", tt.lang)
		}
		if got := codeString(tt.src, spans); got != tt.want {
			t.Errorf("%s %q\n got %s\nwant %s", tt.lang, tt.src, got, tt.want)
		}
	}
	if _, ok := HighlightCode("cobol", "x"); ok {
		t.Error("cobol: want !ok")
	}
}
