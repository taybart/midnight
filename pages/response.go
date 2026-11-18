package pages

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	lua "github.com/taybart/midnight"
	"github.com/taybart/midnight/luaconv"
)

const HTMLContentType = "text/html; charset=utf-8"

// Response is what a script returned, as an HTTP response.
type Response struct {
	Status  int
	Headers map[string]string
	Cookies []*http.Cookie
	Body    []byte
}

// SetDefaultHeader sets a header unless it's already set (case insensitive).
func (res *Response) SetDefaultHeader(key, value string) {
	for k := range res.Headers {
		if strings.EqualFold(k, key) {
			return
		}
	}
	if res.Headers == nil {
		res.Headers = make(map[string]string)
	}
	res.Headers[key] = value
}

// Write writes the response.
func (res Response) Write(w http.ResponseWriter) {
	for k, v := range res.Headers {
		w.Header().Add(k, v)
	}
	for _, c := range res.Cookies {
		http.SetCookie(w, c)
	}
	if w.Header().Get("Content-Length") == "" && w.Header().Get("Content-Encoding") == "" {
		w.Header().Set("Content-Length", strconv.Itoa(len(res.Body)))
	}
	w.WriteHeader(res.Status)
	w.Write(res.Body)
}

// MapResult maps what a script returned to a response: a markup element
// (rendered as an HTML document), or a response table
// ({ status, headers, cookies, body }, where body is a string, a table
// (encoded as json), or an element).
func MapResult(l *lua.LState, v lua.LValue) (Response, error) {
	html, ok, err := RenderDocument(l, v)
	if err != nil {
		return Response{}, err
	}
	if ok {
		res := Response{Status: http.StatusOK, Body: []byte(html)}
		res.SetDefaultHeader("Content-Type", HTMLContentType)
		return res, nil
	}
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return Response{}, fmt.Errorf("expected a response table or markup element, got a %s", v.Type())
	}
	return mapResponseTable(l, tbl)
}

func mapResponseTable(l *lua.LState, luaRes *lua.LTable) (Response, error) {
	res := Response{
		Status: http.StatusOK,
	}
	isHTML, isJSON := false, false
	if cookiesTbl, ok := l.GetField(luaRes, "cookies").(*lua.LTable); ok {
		res.Cookies = luaconv.Cookies(cookiesTbl)
	}
	var mapErr error
	luaRes.ForEach(func(k, v lua.LValue) {
		if mapErr != nil {
			return
		}
		switch strings.ToLower(k.String()) {
		case "status":
			if num, ok := v.(lua.LNumber); ok {
				res.Status = int(num)
			} else {
				mapErr = fmt.Errorf("expected number for status, got %s", v.Type().String())
			}
		case "headers":
			if tbl, ok := v.(*lua.LTable); ok {
				res.Headers = make(map[string]string)
				tbl.ForEach(func(hk, hv lua.LValue) {
					res.Headers[hk.String()] = hv.String()
				})
			} else if v != lua.LNil {
				mapErr = fmt.Errorf("expected table for headers, got %s", v.Type().String())
			}
		case "body":
			// `body = <App/>`
			if html, ok, err := RenderDocument(l, v); err != nil || ok {
				res.Body, isHTML, mapErr = []byte(html), ok, err
				return
			}
			switch b := v.(type) {
			case lua.LString:
				res.Body = []byte(b)
			case *lua.LTable:
				encoded, err := luaconv.EncodeJSON(b)
				if err != nil {
					mapErr = err
					return
				}
				res.Body = []byte(encoded)
				isJSON = true
			case *lua.LNilType:
				// leave empty
			default:
				res.Body = []byte(b.String())
			}
		}
	})
	if isHTML {
		res.SetDefaultHeader("Content-Type", HTMLContentType)
	}
	if isJSON {
		res.SetDefaultHeader("Content-Type", "application/json")
	}
	return res, mapErr
}
