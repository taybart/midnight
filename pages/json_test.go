package pages

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJSONProps(t *testing.T) {
	eng := New(Options{})
	defer eng.Close()
	type repo struct {
		Name string            `json:"name"`
		Tags map[string]string `json:"tags"`
	}
	h := eng.Source(`local { Data } = require('midnight.markup')
	return (p) -> <html><body>
	  <Data id="a" value={p.repos}/>
	  <script type="application/json" id="b">{p.repos}</script>
	  <script define:vars={{ repos = p.repos }}>go(repos)</script>
	  <p>{p.title}</p>
	</body></html>`, Props(func(*http.Request) (any, error) {
		return map[string]any{
			"title": "repos",
			"repos": JSON([]repo{{Name: "</script><b>x", Tags: map[string]string{}}}),
		}, nil
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	// Go's encoding: an empty map stays {}, and < > are escaped
	want := `[{"name":"\u003c/script\u003e\u003cb\u003ex","tags":{}}]`
	for _, id := range []string{"a", "b"} {
		if !strings.Contains(w.Body.String(), `id="`+id+`" type="application/json">`+want+`</script>`) {
			t.Errorf("script %s missing in %s", id, w.Body.String())
		}
	}
	// define:vars: the same JSON, in a js string
	vars := "<script>const repos = JSON.parse('" + strings.ReplaceAll(want, `\`, `\\`) + "');\ngo(repos)</script>"
	if !strings.Contains(w.Body.String(), vars) {
		t.Errorf("define:vars script missing in %s", w.Body.String())
	}
}

func TestJSONErrors(t *testing.T) {
	eng := New(Options{OnError: func(*http.Request, error) {}})
	defer eng.Close()
	h := eng.Source(`local { Data } = require('midnight.markup')
	return (p) -> <html><body><Data value={p.bad}/></body></html>`,
		Props(func(*http.Request) (any, error) {
			return map[string]any{"bad": JSON(make(chan int))}, nil
		}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 500 {
		t.Errorf("unencodable JSON: status %d", w.Code)
	}

	defer func() {
		if recover() == nil {
			t.Error("a JSONValue param should be refused when the handler is built")
		}
	}()
	eng.Source(`return () -> <p/>`, Funcs("app", map[string]any{
		"take": func(context.Context, JSONValue) {},
	}))
}
