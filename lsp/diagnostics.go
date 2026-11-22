package lsp

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/taybart/midnight/format"
	"github.com/taybart/midnight/lint"
	"github.com/taybart/midnight/parse"
)

// Diagnostics are `midnight lint`'s (including syntax errors), published when
// a document opens or changes.
//
// The globals a document may use come from the client: initializationOptions
// `globals`, plus, per document, the `midnight` section of
// `workspace/configuration` (scoped to the document's uri), ex.
// `{ globals = { 'vim' } }`, which midnight.nvim answers w/ the buffer's
// globals (rest's, `vim` in neovim code, vim.b.midnight_globals). When the
// client supports workspace/configuration, a document's diagnostics wait for
// its answer, so they don't flash undefined globals first. Plus the globals in
// the nearest .luarc.json, like the CLI.

type diagnostic struct {
	Range    lspRange `json:"range"`
	Severity int      `json:"severity"` // 1 error, 2 warning
	Code     string   `json:"code,omitempty"`
	Source   string   `json:"source"`
	Message  string   `json:"message"`
}

// docSettings is a document's `midnight` configuration section.
type docSettings struct {
	Globals []string `json:"globals"`
}

type luarcGlobals struct {
	modTime time.Time
	globals []string
}

// requestConfig asks the client for path's settings (see handleResponse).
func (s *Server) requestConfig(path string) {
	s.nextID++
	s.pendingConfig[s.nextID] = path
	err := writeMessage(s.out, map[string]any{
		"jsonrpc": "2.0",
		"id":      s.nextID,
		"method":  "workspace/configuration",
		"params": map[string]any{
			"items": []map[string]any{{"scopeUri": pathToURI(path), "section": "midnight"}},
		},
	})
	if err != nil {
		s.logf("write: %v", err)
	}
}

// handleResponse handles the client's response to a request of ours.
func (s *Server) handleResponse(msg *message) {
	var id int
	if msg.ID == nil || json.Unmarshal(*msg.ID, &id) != nil {
		return
	}
	path, ok := s.pendingConfig[id]
	if !ok {
		return
	}
	delete(s.pendingConfig, id)

	settings := &docSettings{}
	var items []json.RawMessage
	if msg.Error != nil {
		s.logf("workspace/configuration: %s", msg.Error.Message)
	} else if json.Unmarshal(msg.Result, &items) == nil && len(items) > 0 {
		_ = json.Unmarshal(items[0], settings) // (null or not an object: no settings)
	}
	if _, open := s.docs[path]; open {
		s.settings[path] = settings
		s.publish(path)
	}
}

// docChanged lints path, unless its settings are still being requested (then
// it's linted when they arrive).
func (s *Server) docChanged(path string) {
	if _, ok := s.settings[path]; ok || !s.canConfigure {
		s.publish(path)
	}
}

func (s *Server) publish(path string) {
	f := s.file(path)
	if f == nil {
		return
	}
	s.notify("textDocument/publishDiagnostics", map[string]any{
		"uri":         pathToURI(path),
		"diagnostics": s.diagnostics(f),
	})
}

func (s *Server) notify(method string, params any) {
	if err := writeMessage(s.out, map[string]any{"jsonrpc": "2.0", "method": method, "params": params}); err != nil {
		s.logf("write: %v", err)
	}
}

func (s *Server) diagnostics(f *file) []diagnostic {
	diags := []diagnostic{}
	if f.err != nil {
		line, message := 0, f.err.Error()
		var perr *parse.Error
		if errors.As(f.err, &perr) {
			line, message = perr.Pos.Line, strings.TrimSpace(perr.Message)
			if line == parse.EOF {
				line = strings.Count(strings.TrimRight(f.text, "\n"), "\n") + 1
			}
		}
		return append(diags, diagnostic{
			Range: s.lineRange(f.text, line), Severity: 1, Code: "syntax", Source: "midnight", Message: message,
		})
	}

	globals := append([]string{}, s.globals...)
	if settings := s.settings[f.path]; settings != nil {
		globals = append(globals, settings.Globals...)
	}
	globals = append(globals, s.luarcGlobals(filepath.Dir(f.path))...)

	lines := strings.Split(f.text, "\n")
	for _, d := range lint.Check(f.chunk, lint.Options{Globals: globals}) {
		if d.Line >= 1 && d.Line <= len(lines) && strings.Contains(lines[d.Line-1], lint.IgnoreComment) {
			continue
		}
		diags = append(diags, diagnostic{
			Range: s.lineRange(f.text, d.Line), Severity: 2, Code: d.Rule, Source: "midnight", Message: d.Message,
		})
	}
	return diags
}

// lineRange is the (1-based) line's text, w/o its indentation.
func (s *Server) lineRange(text string, line int) lspRange {
	start := 0
	for i := 1; i < line; i++ {
		next := strings.IndexByte(text[start:], '\n')
		if next < 0 {
			break
		}
		start += next + 1
	}
	end := strings.IndexByte(text[start:], '\n')
	if end < 0 {
		end = len(text)
	} else {
		end += start
	}
	for start < end && (text[start] == ' ' || text[start] == '\t') {
		start++
	}
	return toRange(text, start, end, s.encoding)
}

// luarcGlobals returns the globals in the .luarc.json nearest dir (cached by
// modification time).
func (s *Server) luarcGlobals(dir string) []string {
	path := lint.FindLuarc(dir)
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if c, ok := s.luarcs[path]; ok && c.modTime.Equal(info.ModTime()) {
		return c.globals
	}
	globals, err := lint.LuarcGlobals(path)
	if err != nil {
		s.logf("%v", err)
	}
	s.luarcs[path] = luarcGlobals{modTime: info.ModTime(), globals: globals}
	return globals
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

// formatting is `midnight fmt`, as one edit replacing the document (nil when
// it doesn't parse).
func (s *Server) formatting(path string) []textEdit {
	f := s.file(path)
	if f == nil {
		return nil
	}
	out, err := format.Format([]byte(f.text), format.Options{})
	if err != nil {
		s.logf("format %s: %v", path, err)
		return nil
	}
	if string(out) == f.text {
		return []textEdit{}
	}
	return []textEdit{{Range: toRange(f.text, 0, len(f.text), s.encoding), NewText: string(out)}}
}
