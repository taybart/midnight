package lint

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// parseJSONC decodes JSON w/ comments (`//`, `/* */`) and trailing commas,
// like lua-language-server accepts in .luarc.json, into map[string]any,
// []any, string, float64, bool, or nil.
//
// It's here instead of encoding/json, which adds ~1MB to the binary for
// reading one config file.
func parseJSONC(data []byte) (any, error) {
	p := &jsoncParser{data: data}
	v, err := p.value()
	if err != nil {
		return nil, err
	}
	if p.skip(); p.pos < len(p.data) {
		return nil, p.errorf("unexpected %q after value", p.data[p.pos])
	}
	return v, nil
}

type jsoncParser struct {
	data []byte
	pos  int
}

func (p *jsoncParser) errorf(format string, args ...any) error {
	line := 1
	for _, c := range p.data[:min(p.pos, len(p.data))] {
		if c == '\n' {
			line++
		}
	}
	return fmt.Errorf("line %d: "+format, append([]any{line}, args...)...)
}

// skip skips whitespace and comments.
func (p *jsoncParser) skip() {
	for p.pos < len(p.data) {
		switch c := p.data[p.pos]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '/':
			for p.pos < len(p.data) && p.data[p.pos] != '\n' {
				p.pos++
			}
		case c == '/' && p.pos+1 < len(p.data) && p.data[p.pos+1] == '*':
			p.pos += 2
			for p.pos+1 < len(p.data) && !(p.data[p.pos] == '*' && p.data[p.pos+1] == '/') {
				p.pos++
			}
			p.pos += 2
		default:
			return
		}
	}
}

func (p *jsoncParser) value() (any, error) {
	p.skip()
	if p.pos >= len(p.data) {
		return nil, p.errorf("unexpected end of input")
	}
	switch c := p.data[p.pos]; {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		return p.string()
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	}
	for word, v := range map[string]any{"true": true, "false": false, "null": nil} {
		if end := p.pos + len(word); end <= len(p.data) && string(p.data[p.pos:end]) == word {
			p.pos = end
			return v, nil
		}
	}
	return nil, p.errorf("unexpected %q", p.data[p.pos])
}

// list parses the items of an object or array up to close, allowing a
// trailing comma.
func (p *jsoncParser) list(close byte, item func() error) error {
	p.pos++ // the opening bracket
	for {
		if p.skip(); p.pos >= len(p.data) {
			return p.errorf("expected %q", close)
		} else if p.data[p.pos] == close {
			p.pos++
			return nil
		}
		if err := item(); err != nil {
			return err
		}
		p.skip()
		if p.pos >= len(p.data) {
			return p.errorf("expected %q", close)
		}
		switch p.data[p.pos] {
		case ',':
			p.pos++
		case close:
		default:
			return p.errorf("expected ',' or %q", close)
		}
	}
}

func (p *jsoncParser) object() (any, error) {
	obj := map[string]any{}
	err := p.list('}', func() error {
		if p.data[p.pos] != '"' {
			return p.errorf("expected a key")
		}
		key, err := p.string()
		if err != nil {
			return err
		}
		if p.skip(); p.pos >= len(p.data) || p.data[p.pos] != ':' {
			return p.errorf("expected ':'")
		}
		p.pos++
		obj[key], err = p.value()
		return err
	})
	return obj, err
}

func (p *jsoncParser) array() (any, error) {
	arr := []any{}
	err := p.list(']', func() error {
		v, err := p.value()
		arr = append(arr, v)
		return err
	})
	return arr, err
}

func (p *jsoncParser) number() (any, error) {
	start := p.pos
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		if !(c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E') {
			break
		}
		p.pos++
	}
	n, err := strconv.ParseFloat(string(p.data[start:p.pos]), 64)
	if err != nil {
		return nil, p.errorf("invalid number %q", p.data[start:p.pos])
	}
	return n, nil
}

func (p *jsoncParser) string() (string, error) {
	p.pos++ // the opening quote
	var out []byte
	for p.pos < len(p.data) {
		c := p.data[p.pos]
		p.pos++
		switch {
		case c == '"':
			return string(out), nil
		case c != '\\':
			out = append(out, c)
		case p.pos >= len(p.data):
			return "", p.errorf("unterminated string")
		default:
			esc := p.data[p.pos]
			p.pos++
			switch esc {
			case '"', '\\', '/':
				out = append(out, esc)
			case 'b':
				out = append(out, '\b')
			case 'f':
				out = append(out, '\f')
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'u':
				r, err := p.hex4()
				if err != nil {
					return "", err
				}
				// a surrogate pair (ex. emoji) is two \u escapes
				if r >= 0xD800 && r < 0xDC00 && p.pos+1 < len(p.data) && p.data[p.pos] == '\\' && p.data[p.pos+1] == 'u' {
					p.pos += 2
					lo, err := p.hex4()
					if err != nil {
						return "", err
					}
					r = 0x10000 + (r-0xD800)<<10 + (lo - 0xDC00)
				}
				out = utf8.AppendRune(out, r)
			default:
				return "", p.errorf("invalid escape '\\%c'", esc)
			}
		}
	}
	return "", p.errorf("unterminated string")
}

func (p *jsoncParser) hex4() (rune, error) {
	if p.pos+4 > len(p.data) {
		return 0, p.errorf("invalid \\u escape")
	}
	n, err := strconv.ParseUint(string(p.data[p.pos:p.pos+4]), 16, 32)
	if err != nil {
		return 0, p.errorf("invalid \\u escape")
	}
	p.pos += 4
	return rune(n), nil
}
