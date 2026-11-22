package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// Position encodings: utf-8 when the client supports it (columns are byte
// offsets, like ours), else LSP's default utf-16.
const (
	encodingUTF8  = "utf-8"
	encodingUTF16 = "utf-16"
)

// toPosition converts a byte offset in text to an LSP position.
func toPosition(text string, offset int, encoding string) position {
	offset = min(max(offset, 0), len(text))
	line := strings.Count(text[:offset], "\n")
	lineStart := strings.LastIndexByte(text[:offset], '\n') + 1
	return position{Line: line, Character: columnUnits(text[lineStart:offset], encoding)}
}

// toOffset converts an LSP position to a byte offset in text (clamped to the
// line / text).
func toOffset(text string, pos position, encoding string) int {
	offset := 0
	for line := 0; line < pos.Line; line++ {
		i := strings.IndexByte(text[offset:], '\n')
		if i < 0 {
			return len(text)
		}
		offset += i + 1
	}
	lineEnd := strings.IndexByte(text[offset:], '\n')
	if lineEnd < 0 {
		lineEnd = len(text) - offset
	}
	lineText := text[offset : offset+lineEnd]
	if encoding == encodingUTF8 {
		return offset + min(pos.Character, len(lineText))
	}
	units := 0
	for i, r := range lineText {
		if units >= pos.Character {
			return offset + i
		}
		units += utf16.RuneLen(r)
	}
	return offset + len(lineText)
}

func columnUnits(s, encoding string) int {
	if encoding == encodingUTF8 {
		return len(s)
	}
	units := 0
	for _, r := range s {
		if r == utf8.RuneError {
			units++
			continue
		}
		units += utf16.RuneLen(r)
	}
	return units
}

func toRange(text string, start, end int, encoding string) lspRange {
	return lspRange{Start: toPosition(text, start, encoding), End: toPosition(text, end, encoding)}
}

func uriToPath(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return ""
	}
	return filepath.FromSlash(u.Path)
}

func pathToURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}
