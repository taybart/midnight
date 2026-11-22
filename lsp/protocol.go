package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/textproto"
	"strconv"
	"strings"
)

// JSON-RPC 2.0 over stdio, framed w/ `Content-Length` headers (the LSP base
// protocol).

// message is a request or notification from the client, or (w/o a method)
// its response to a request of ours.
type message struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *responseError   `json:"error,omitempty"`
}

// response is a successful response (result may be null). Errors are
// errorResponse, since a response can't have both.
type response struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Result  any              `json:"result"`
}

type errorResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id"`
	Error   *responseError   `json:"error"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func readMessage(r *bufio.Reader) (*message, error) {
	header, err := textproto.NewReader(r).ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	length, err := strconv.Atoi(strings.TrimSpace(header.Get("Content-Length")))
	if err != nil {
		return nil, fmt.Errorf("bad Content-Length: %w", err)
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	msg := &message{}
	if err := json.Unmarshal(body, msg); err != nil {
		return nil, fmt.Errorf("bad message: %w", err)
	}
	return msg, nil
}

func writeMessage(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "Content-Length: %d\r\n\r\n%s", len(body), body)
	return err
}

/* LSP types (only the fields used) {{{ */

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentPositionParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
	Position     position               `json:"position"`
}

type referenceParams struct {
	textDocumentPositionParams
	Context struct {
		IncludeDeclaration bool `json:"includeDeclaration"`
	} `json:"context"`
}

type initializeParams struct {
	RootURI      string `json:"rootUri"`
	Capabilities struct {
		General struct {
			PositionEncodings []string `json:"positionEncodings"`
		} `json:"general"`
		Workspace struct {
			Configuration bool `json:"configuration"`
		} `json:"workspace"`
	} `json:"capabilities"`
	// (raw: clients may send anything, ex. neovim sends `[]` for an empty
	// table, see initializationOptions)
	InitializationOptions json.RawMessage `json:"initializationOptions"`
}

type initializationOptions struct {
	MarkupFactory string   `json:"markupFactory"`
	Globals       []string `json:"globals"`
}

type didOpenParams struct {
	TextDocument struct {
		URI  string `json:"uri"`
		Text string `json:"text"`
	} `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument   textDocumentIdentifier `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type didCloseParams struct {
	TextDocument textDocumentIdentifier `json:"textDocument"`
}

type documentHighlight struct {
	Range lspRange `json:"range"`
	Kind  int      `json:"kind"` // 1 text, 2 read, 3 write
}

/* }}} */
