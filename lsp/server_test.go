package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mainSrc = `const m = require('lib.util')
local { greet } = require('lib.util')
const old = require('lib.old')
local x = 1
print(x, m.add(1, 2), greet("a"), shared, old.thing())
print("é {x}")
`

var workspace = map[string]string{
	"main.mn":     mainSrc,
	"lib/util.mn": "module function add(a, b) { return a + b }\nmodule function greet(n) { return \"hi {n}\" }\n",
	"lib/old.lua": "local M = {}\nfunction M.thing() end\nreturn M\n",
	"other.mn":    "global shared = 1\n",
}

func setup(t *testing.T) string {
	root := t.TempDir()
	for name, text := range workspace {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// session sends requests (in order, ids from 1) after initialize / didOpen
// and returns the responses by id.
func session(t *testing.T, root string, encodings []string, requests []map[string]any) map[int]json.RawMessage {
	in := &bytes.Buffer{}
	send := func(v map[string]any) {
		v["jsonrpc"] = "2.0"
		if err := writeMessage(in, v); err != nil {
			t.Fatal(err)
		}
	}
	send(map[string]any{"id": 0, "method": "initialize", "params": map[string]any{
		"rootUri":               pathToURI(root),
		"capabilities":          map[string]any{"general": map[string]any{"positionEncodings": encodings}},
		"initializationOptions": []any{}, // (neovim's empty table)
	}})
	send(map[string]any{"method": "initialized", "params": map[string]any{}})
	send(map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(filepath.Join(root, "main.mn")), "text": mainSrc},
	}})
	for i, req := range requests {
		req["id"] = i + 1
		send(req)
	}
	send(map[string]any{"id": 999, "method": "shutdown"})
	send(map[string]any{"method": "exit"})

	out, log := &bytes.Buffer{}, &bytes.Buffer{}
	if err := Serve(in, out, log); err != nil {
		t.Fatal(err)
	}
	// (decode the raw frames: readMessage only reads requests)
	responses := map[int]json.RawMessage{}
	for _, frame := range strings.Split(out.String(), "Content-Length: ")[1:] {
		body := frame[strings.Index(frame, "\r\n\r\n")+4:]
		var resp struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *responseError  `json:"error"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatalf("bad response %q: %v", body, err)
		}
		if resp.Error != nil {
			t.Errorf("id %d: error %s", resp.ID, resp.Error.Message)
		}
		responses[resp.ID] = resp.Result
	}
	if log.Len() > 0 {
		t.Logf("log: %s", log)
	}
	return responses
}

// at returns the position of the n-th (from 0) occurrence of word in text,
// plus offset bytes.
func at(t *testing.T, text, word string, n, offset int, encoding string) position {
	i := -1
	for range n + 1 {
		j := strings.Index(text[i+1:], word)
		if j < 0 {
			t.Fatalf("%q #%d not found", word, n)
		}
		i += j + 1
	}
	return toPosition(text, i+offset, encoding)
}

func request(method, path string, pos position) map[string]any {
	return map[string]any{"method": method, "params": map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path)},
		"position":     pos,
		"context":      map[string]any{"includeDeclaration": true},
	}}
}

func TestDefinition(t *testing.T) {
	root := setup(t)
	main := filepath.Join(root, "main.mn")

	tests := []struct {
		name     string
		word     string
		n        int
		wantFile string
		wantWord string // n-th occurrence of wantWord in wantFile
		wantN    int
	}{
		{"local", "x, m", 0, "main.mn", "x = 1", 0},
		{"module field", "add", 0, "lib/util.mn", "add", 0},
		{"destructured require", "greet", 1, "lib/util.mn", "greet", 0},
		{"require decl", "greet", 0, "lib/util.mn", "greet", 0},
		{"lua module field", "thing", 0, "lib/old.lua", "thing", 0},
		{"workspace global", "shared", 0, "other.mn", "shared", 0},
		{"interpolation after utf-8", "{x}", 0, "main.mn", "x = 1", 0},
	}

	var requests []map[string]any
	for _, tt := range tests {
		offset := 0
		if strings.HasPrefix(tt.word, "{") {
			offset = 1
		}
		requests = append(requests, request("textDocument/definition", main, at(t, mainSrc, tt.word, tt.n, offset, encodingUTF16)))
	}
	responses := session(t, root, []string{"utf-16"}, requests)

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var locs []location
			if err := json.Unmarshal(responses[i+1], &locs); err != nil || len(locs) != 1 {
				t.Fatalf("got %s (%v)", responses[i+1], err)
			}
			wantPath := filepath.Join(root, tt.wantFile)
			text := workspace[tt.wantFile]
			want := at(t, text, tt.wantWord, tt.wantN, 0, encodingUTF16)
			if got := uriToPath(locs[0].URI); got != wantPath {
				t.Errorf("file: got %s, want %s", got, wantPath)
			}
			if locs[0].Range.Start != want {
				t.Errorf("position: got %+v, want %+v", locs[0].Range.Start, want)
			}
		})
	}
}

func TestReferencesAndHighlights(t *testing.T) {
	root := setup(t)
	main := filepath.Join(root, "main.mn")
	pos := at(t, mainSrc, "local x", 0, len("local "), encodingUTF8)
	responses := session(t, root, []string{"utf-8", "utf-16"}, []map[string]any{
		request("textDocument/references", main, pos),
		request("textDocument/documentHighlight", main, pos),
	})
	var locs []location
	if err := json.Unmarshal(responses[1], &locs); err != nil {
		t.Fatal(err)
	}
	if len(locs) != 3 { // declaration, print(x, "{x}"
		t.Errorf("references: got %d, want 3: %s", len(locs), responses[1])
	}
	var highlights []documentHighlight
	if err := json.Unmarshal(responses[2], &highlights); err != nil {
		t.Fatal(err)
	}
	if len(highlights) != 3 || highlights[0].Kind != 3 {
		t.Errorf("highlights: got %s", responses[2])
	}
	// utf-8 columns are bytes: the `x` in "é {x}" is at byte 10
	if last := highlights[2].Range.Start; last.Character != len(`print("é {`) {
		t.Errorf("utf-8 column: got %d", last.Character)
	}
}

func TestPositions(t *testing.T) {
	text := "ab\n\"é😀x\"\n"
	off := strings.Index(text, "x")
	for _, enc := range []string{encodingUTF8, encodingUTF16} {
		pos := toPosition(text, off, enc)
		if got := toOffset(text, pos, enc); got != off {
			t.Errorf("%s: %+v -> %d, want %d", enc, pos, got, off)
		}
	}
	if pos := toPosition(text, off, encodingUTF16); pos.Character != 4 { // " é 😀(2)
		t.Errorf("utf-16: got %d, want 4", pos.Character)
	}
}

func TestInlineRequire(t *testing.T) {
	text := "x = 1\nrequire('a.b').setup(1) + require \"c\":go()\n"
	tests := []struct {
		word     string
		mod, key string
		ok       bool
	}{
		{"a.b", "a.b", "", true},
		{"setup", "a.b", "setup", true},
		{"go", "c", "go", true},
		{"1)", "", "", false},
		{"x", "", "", false},
	}
	for _, tt := range tests {
		mod, key, ok := inlineRequire(text, strings.Index(text, tt.word))
		if mod != tt.mod || key != tt.key || ok != tt.ok {
			t.Errorf("%s: got %q %q %v", tt.word, mod, key, ok)
		}
	}
}

func TestErrorResponseHasNoResult(t *testing.T) {
	out := &bytes.Buffer{}
	s := &Server{out: out, log: &bytes.Buffer{}}
	id := json.RawMessage(`7`)
	s.replyError(&id, codeMethodNotFound, "nope")
	if strings.Contains(out.String(), `"result"`) {
		t.Errorf("error response has a result: %s", out)
	}
}
