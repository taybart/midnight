package lsp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run sends msgs (then shutdown / exit) and returns everything the server
// wrote.
func run(t *testing.T, msgs ...map[string]any) []map[string]any {
	in := &bytes.Buffer{}
	for _, m := range append(msgs, map[string]any{"id": 999, "method": "shutdown"}, map[string]any{"method": "exit"}) {
		m["jsonrpc"] = "2.0"
		if err := writeMessage(in, m); err != nil {
			t.Fatal(err)
		}
	}
	out, log := &bytes.Buffer{}, &bytes.Buffer{}
	if err := Serve(in, out, log); err != nil {
		t.Fatal(err)
	}
	var frames []map[string]any
	for _, frame := range strings.Split(out.String(), "Content-Length: ")[1:] {
		var v map[string]any
		if err := json.Unmarshal([]byte(frame[strings.Index(frame, "\r\n\r\n")+4:]), &v); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, v)
	}
	return frames
}

func initialize(root string, configuration bool, options any) map[string]any {
	return map[string]any{"id": 0, "method": "initialize", "params": map[string]any{
		"rootUri": pathToURI(root),
		"capabilities": map[string]any{
			"general":   map[string]any{"positionEncodings": []string{"utf-8"}},
			"workspace": map[string]any{"configuration": configuration},
		},
		"initializationOptions": options,
	}}
}

func didOpen(path, text string) map[string]any {
	return map[string]any{"method": "textDocument/didOpen", "params": map[string]any{
		"textDocument": map[string]any{"uri": pathToURI(path), "text": text},
	}}
}

func didChange(path, text string) map[string]any {
	return map[string]any{"method": "textDocument/didChange", "params": map[string]any{
		"textDocument":   map[string]any{"uri": pathToURI(path)},
		"contentChanges": []any{map[string]any{"text": text}},
	}}
}

// published returns the messages of each publishDiagnostics, in order.
func published(frames []map[string]any) [][]string {
	var all [][]string
	for _, f := range frames {
		if f["method"] != "textDocument/publishDiagnostics" {
			continue
		}
		messages := []string{}
		for _, d := range f["params"].(map[string]any)["diagnostics"].([]any) {
			d := d.(map[string]any)
			line := int(d["range"].(map[string]any)["start"].(map[string]any)["line"].(float64))
			messages = append(messages, d["code"].(string)+"@"+string(rune('0'+line)))
		}
		all = append(all, messages)
	}
	return all
}

const lintSrc = "print(vim, host)\nlocal unused = 1\nprint(skip) -- midnight-lint: ignore\n"

func TestDiagnosticsWithoutConfiguration(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.mn")
	frames := run(t,
		initialize(root, false, map[string]any{"globals": []string{"host"}}),
		didOpen(path, lintSrc),
		didChange(path, "local = 1\n"),
	)
	got := published(frames)
	want := [][]string{{"undefined-global@0", "unused-local@1"}, {"syntax@0"}}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestDiagnosticsWaitForConfiguration(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".luarc.json"), []byte(`{"diagnostics": {"globals": ["host"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "a.mn")
	frames := run(t,
		initialize(root, true, []any{}),
		didOpen(path, lintSrc),
		// (before the settings arrive: not linted yet)
		didChange(path, lintSrc+"print(late)\n"),
		map[string]any{"id": 1, "result": []any{map[string]any{"globals": []string{"vim"}}}},
		didChange(path, lintSrc),
	)

	var request map[string]any
	for _, f := range frames {
		if f["method"] == "workspace/configuration" {
			request = f
		}
	}
	if request == nil || request["id"].(float64) != 1 {
		t.Fatalf("no workspace/configuration request: %v", frames)
	}
	got := published(frames)
	want := [][]string{{"unused-local@1", "undefined-global@3"}, {"unused-local@1"}}
	if !equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFormatting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "a.mn")
	format := func(id int) map[string]any {
		return map[string]any{"id": id, "method": "textDocument/formatting", "params": map[string]any{
			"textDocument": map[string]any{"uri": pathToURI(path)}, "options": map[string]any{"tabSize": 2},
		}}
	}
	frames := run(t,
		initialize(root, false, nil),
		didOpen(path, "local   x=1\n"), format(1),
		didChange(path, "local x = 1\n"), format(2),
		didChange(path, "local = \n"), format(3),
	)
	results := map[float64]any{}
	for _, f := range frames {
		if id, ok := f["id"].(float64); ok {
			results[id] = f["result"]
		}
	}
	edits, _ := results[1].([]any)
	if len(edits) != 1 || edits[0].(map[string]any)["newText"] != "local x = 1\n" {
		t.Errorf("format: got %v", results[1])
	}
	if edits, ok := results[2].([]any); !ok || len(edits) != 0 {
		t.Errorf("already formatted: got %v", results[2])
	}
	if results[3] != nil {
		t.Errorf("syntax error: got %v", results[3])
	}
}

func equal(a, b [][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.Join(a[i], ",") != strings.Join(b[i], ",") {
			return false
		}
	}
	return true
}
