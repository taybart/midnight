package parse

import (
	"strings"
	"testing"
)

func hlString(text string, spans []HighlightSpan) string {
	var b strings.Builder
	for _, s := range spans {
		if s.Kind == HlPlain {
			b.WriteString(text[s.Start:s.End])
			continue
		}
		b.WriteString("[" + s.Kind + ":" + text[s.Start:s.End] + "]")
	}
	return b.String()
}

func TestMidnightHighlight(t *testing.T) {
	tests := []struct{ name, src, want string }{
		{"code",
			`local x = 0x1F -- hi`,
			`[keyword:local] [word:x] [symbol:=] [number:0x1F] [comment:-- hi]`},
		{"constants and operators",
			`if a != nil && !b { return true }`,
			`[keyword:if] [word:a] [symbol:!=] [constant:nil] [symbol:&&] [symbol:!][word:b] [symbol:{] [keyword:return] [constant:true] [symbol:}]`},
		{"interpolation",
			`"hi {name:upper()}!"`,
			`[string:"hi ][interp:{][word:name][symbol::][word:upper][symbol:(][symbol:)][interp:}][string:!"]`},
		{"format spec",
			`"{p:%.2f}"`,
			`[string:"][interp:{][word:p][format::%.2f][interp:}][string:"]`},
		{"single quotes don't interpolate",
			`'a {b}'`,
			`[string:'a {b}']`},
		{"markup",
			`<a href={url} class="x">don't {n}</a>`,
			`[markup:<][tag:a] [attr:href][markup:=][markup:{][word:url][markup:}] [attr:class][markup:=][string:"x"][markup:>][text:don't ][markup:{][word:n][markup:}][markup:</][tag:a][markup:>]`},
		{"self closing component",
			`return <Card {...props}/>`,
			`[keyword:return] [markup:<][tag:Card] [markup:{][symbol:...][word:props][markup:}][markup:/>]`},
		{"raw text",
			`<style>a { color: red }</style>`,
			`[markup:<][tag:style][markup:>][raw:a { color: red }][markup:</][tag:style][markup:>]`},
		{"comparison isn't markup",
			`a <b`,
			`[word:a] [symbol:<][word:b]`},
		{"shebang",
			"#!/bin/midnight\nx",
			"[comment:#!/bin/midnight]\n[word:x]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans, err := MidnightHighlight(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			if got := hlString(tt.src, spans); got != tt.want {
				t.Errorf("\n got: %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestMidnightHighlightCoversSource(t *testing.T) {
	src := "local s = [[a {b} c]]\n<div>\n  {map(xs, (x) -> <li>{x}</li>)}\n</div>\n"
	spans, err := MidnightHighlight(src)
	if err != nil {
		t.Fatal(err)
	}
	pos := 0
	for _, s := range spans {
		if s.Start != pos || s.End <= s.Start {
			t.Fatalf("span %+v doesn't continue at %d", s, pos)
		}
		pos = s.End
	}
	if pos != len(src) {
		t.Fatalf("spans end at %d, source is %d", pos, len(src))
	}
}

func TestMidnightHighlightError(t *testing.T) {
	src := "local a = 'oops\nlocal b = 1"
	spans, err := MidnightHighlight(src)
	if err == nil {
		t.Fatal("expected an error")
	}
	got := hlString(src, spans)
	want := "[keyword:local] [word:a] [symbol:=] [string:'oops]\nlocal b = 1"
	if got != want {
		t.Errorf("\n got: %q\nwant: %q", got, want)
	}
}
