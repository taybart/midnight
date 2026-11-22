// Package lsp is Midnight's language server (`midnight lsp`): diagnostics
// (`midnight lint`), formatting (`midnight fmt`), go to definition (in the
// file, through `require`, and to `global`s elsewhere in the workspace),
// references, and document highlights.
package lsp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"

	"github.com/taybart/midnight/parse"
)

type Server struct {
	out io.Writer
	log io.Writer

	root     string // workspace root (from rootUri), or ""
	encoding string
	opts     parse.MidnightOptions
	globals  []string // from initializationOptions

	// whether the client answers workspace/configuration (see diagnostics.go)
	canConfigure bool
	// per document settings, by path, and the requests for them, by id
	settings      map[string]*docSettings
	pendingConfig map[int]string
	nextID        int
	luarcs        map[string]luarcGlobals

	// open documents, by path
	docs map[string]string
	// parsed files, by path (see analysis.go)
	cache map[string]*cachedFile

	shutdown bool
}

// Serve runs a server on in / out (normally stdin / stdout), logging to log,
// until the client says `exit` or in closes.
func Serve(in io.Reader, out, log io.Writer) error {
	s := &Server{
		out:           out,
		log:           log,
		encoding:      encodingUTF16,
		docs:          map[string]string{},
		cache:         map[string]*cachedFile{},
		settings:      map[string]*docSettings{},
		pendingConfig: map[int]string{},
		luarcs:        map[string]luarcGlobals{},
	}
	r := bufio.NewReader(in)
	for {
		msg, err := readMessage(r)
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if msg.Method == "exit" {
			return nil
		}
		s.handle(msg)
	}
}

func (s *Server) logf(format string, args ...any) {
	fmt.Fprintf(s.log, "midnight lsp: "+format+"\n", args...)
}

func (s *Server) reply(id *json.RawMessage, result any) {
	if err := writeMessage(s.out, response{JSONRPC: "2.0", ID: id, Result: result}); err != nil {
		s.logf("write: %v", err)
	}
}

func (s *Server) replyError(id *json.RawMessage, code int, message string) {
	err := writeMessage(s.out, errorResponse{JSONRPC: "2.0", ID: id, Error: &responseError{Code: code, Message: message}})
	if err != nil {
		s.logf("write: %v", err)
	}
}

// params decodes msg's params into v, replying w/ an error (for requests) if
// they don't decode.
func (s *Server) params(msg *message, v any) bool {
	if err := json.Unmarshal(msg.Params, v); err != nil {
		if msg.ID != nil {
			s.replyError(msg.ID, codeInvalidParams, err.Error())
		}
		s.logf("%s: bad params: %v", msg.Method, err)
		return false
	}
	return true
}

func (s *Server) handle(msg *message) {
	if msg.Method == "" {
		s.handleResponse(msg)
		return
	}
	switch msg.Method {
	case "initialize":
		var p initializeParams
		if !s.params(msg, &p) {
			return
		}
		s.initialize(p)
		s.reply(msg.ID, map[string]any{
			"capabilities": map[string]any{
				"positionEncoding":           s.encoding,
				"textDocumentSync":           map[string]any{"openClose": true, "change": 1}, // full
				"definitionProvider":         true,
				"documentFormattingProvider": true,
				"referencesProvider":         true,
				"documentHighlightProvider":  true,
			},
			"serverInfo": map[string]any{"name": "midnight"},
		})

	case "shutdown":
		s.shutdown = true
		s.reply(msg.ID, nil)

	case "textDocument/didOpen":
		var p didOpenParams
		if !s.params(msg, &p) {
			return
		}
		path := uriToPath(p.TextDocument.URI)
		s.docs[path] = p.TextDocument.Text
		if s.canConfigure {
			s.requestConfig(path)
		} else {
			s.publish(path)
		}
	case "textDocument/didChange":
		var p didChangeParams
		if s.params(msg, &p) && len(p.ContentChanges) > 0 {
			path := uriToPath(p.TextDocument.URI)
			s.docs[path] = p.ContentChanges[len(p.ContentChanges)-1].Text
			s.docChanged(path)
		}
	case "textDocument/didClose":
		var p didCloseParams
		if s.params(msg, &p) {
			path := uriToPath(p.TextDocument.URI)
			delete(s.docs, path)
			delete(s.settings, path)
			s.notify("textDocument/publishDiagnostics", map[string]any{"uri": p.TextDocument.URI, "diagnostics": []diagnostic{}})
		}
	case "workspace/didChangeConfiguration":
		// (re-ask for each document's settings, ex. its globals)
		if s.canConfigure {
			for path := range s.docs {
				s.requestConfig(path)
			}
		}

	case "textDocument/formatting":
		var p struct {
			TextDocument textDocumentIdentifier `json:"textDocument"`
		}
		if s.params(msg, &p) {
			s.reply(msg.ID, s.formatting(uriToPath(p.TextDocument.URI)))
		}

	case "textDocument/definition":
		var p textDocumentPositionParams
		if !s.params(msg, &p) {
			return
		}
		f, offset := s.at(p)
		if f == nil {
			s.reply(msg.ID, nil)
			return
		}
		s.reply(msg.ID, s.locations(s.definition(f, offset)))

	case "textDocument/references":
		var p referenceParams
		if !s.params(msg, &p) {
			return
		}
		f, offset := s.at(p.textDocumentPositionParams)
		if f == nil {
			s.reply(msg.ID, nil)
			return
		}
		spans := references(f, offset)
		if !p.Context.IncludeDeclaration && len(spans) > 0 {
			spans = spans[1:]
		}
		s.reply(msg.ID, s.locations(spans))

	case "textDocument/documentHighlight":
		var p textDocumentPositionParams
		if !s.params(msg, &p) {
			return
		}
		f, offset := s.at(p)
		if f == nil {
			s.reply(msg.ID, nil)
			return
		}
		highlights := []documentHighlight{}
		for i, sp := range references(f, offset) {
			kind := 2 // read
			if i == 0 {
				kind = 3 // write (the declaration)
			}
			highlights = append(highlights, documentHighlight{Range: toRange(f.text, sp.start, sp.end, s.encoding), Kind: kind})
		}
		s.reply(msg.ID, highlights)

	case "initialized", "$/cancelRequest", "$/setTrace":
		// nothing to do

	default:
		if msg.ID != nil {
			s.replyError(msg.ID, codeMethodNotFound, "unsupported method "+msg.Method)
		}
	}
}

func (s *Server) initialize(p initializeParams) {
	if p.RootURI != "" {
		s.root = uriToPath(p.RootURI)
	}
	if slices.Contains(p.Capabilities.General.PositionEncodings, encodingUTF8) {
		s.encoding = encodingUTF8
	}
	s.canConfigure = p.Capabilities.Workspace.Configuration
	var opts initializationOptions
	_ = json.Unmarshal(p.InitializationOptions, &opts) // (anything but an object is no options)
	s.globals = opts.Globals
	if name := opts.MarkupFactory; name != "" {
		if err := s.opts.SetMarkupFactory(name); err != nil {
			s.logf("markupFactory: %v", err)
		}
	}
}

// at returns the analyzed file and byte offset for a position, or nil if the
// file isn't Midnight (or can't be read).
func (s *Server) at(p textDocumentPositionParams) (*file, int) {
	path := uriToPath(p.TextDocument.URI)
	if path == "" || !parse.IsMidnightPath(path) && s.docs[path] == "" {
		return nil, 0
	}
	f := s.file(path)
	if f == nil {
		return nil, 0
	}
	return f, toOffset(f.text, p.Position, s.encoding)
}

// locations converts spans to LSP locations (null when there are none).
func (s *Server) locations(spans []span) any {
	if len(spans) == 0 {
		return nil
	}
	locs := []location{}
	for _, sp := range spans {
		text := sp.text
		if text == "" && sp.end > 0 {
			if data, err := os.ReadFile(sp.path); err == nil {
				text = string(data)
			}
		}
		locs = append(locs, location{URI: pathToURI(sp.path), Range: toRange(text, sp.start, sp.end, s.encoding)})
	}
	return locs
}
