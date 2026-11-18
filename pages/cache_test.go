package pages_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/taybart/midnight/pages"
)

// counted serves src with Cache, with `renders.inc()` counting how many times the
// page really ran.
func counted(t *testing.T, src string, opts ...pages.Option) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	eng := pages.New(pages.Options{OnError: func(_ *http.Request, err error) { t.Error(err) }})
	t.Cleanup(eng.Close)
	opts = append(opts,
		pages.Funcs("renders", map[string]any{"inc": func() int { return int(n.Add(1)) }}),
		queryFuncs,
	)
	ts := httptest.NewServer(eng.Source(src, opts...))
	t.Cleanup(ts.Close)
	return ts, &n
}

// queryFuncs is `require('req').query(name)`, the request's query param.
var queryFuncs = pages.Funcs("req", map[string]any{
	"query": func(r *http.Request, name string) string { return r.URL.Query().Get(name) },
})

func getWith(t *testing.T, url string, header ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return res, string(b)
}

// A static page is rendered once per path + query + vary headers.
func TestCacheStatic(t *testing.T) {
	ts, n := counted(t, `
local renders = require('renders')
return <p>render {renders.inc()} {require('req').query('q')}</p>`, pages.Cache(time.Minute, "Accept-Language"))

	for range 3 {
		res, html := getWith(t, ts.URL+"/?q=a")
		if !strings.Contains(html, "render 1 a") {
			t.Fatalf("cached: %s", html)
		}
		if cc := res.Header.Get("Cache-Control"); cc != "public, max-age=60" {
			t.Errorf("Cache-Control %q", cc)
		}
	}
	if _, html := getWith(t, ts.URL+"/?q=b"); !strings.Contains(html, "render 2 b") {
		t.Errorf("other query: %s", html)
	}
	if _, html := getWith(t, ts.URL+"/?q=a", "Accept-Language", "fr"); !strings.Contains(html, "render 3 a") {
		t.Errorf("vary header: %s", html)
	}
	if _, html := getWith(t, ts.URL+"/?q=a", "Accept-Language", "fr"); !strings.Contains(html, "render 3 a") {
		t.Errorf("vary header, cached: %s", html)
	}
	if n.Load() != 3 {
		t.Errorf("rendered %d times, want 3", n.Load())
	}
}

// Cached responses expire.
func TestCacheExpires(t *testing.T) {
	ts, n := counted(t, `return <p>{require('renders').inc()}</p>`, pages.Cache(30*time.Millisecond))
	getWith(t, ts.URL)
	getWith(t, ts.URL)
	time.Sleep(50 * time.Millisecond)
	getWith(t, ts.URL)
	if n.Load() != 2 {
		t.Errorf("rendered %d times, want 2", n.Load())
	}
}

// What isn't cached: interactive pages (each visitor needs their own state),
// non-200s, and responses with cookies. A page's own Cache-Control is kept.
func TestCacheSkips(t *testing.T) {
	for name, c := range map[string]struct {
		src     string
		renders int32
	}{
		"interactive": {`
local n = require('renders').inc()
local App = () -> <button onclick={() -> {}}>{n}</button>
return <App/>`, 3},
		"redirect": {`require('renders').inc() return { status = 302, headers = { Location = '/x' } }`, 3},
		"cookies":  {`require('renders').inc() return { body = <p/>, cookies = { { name = 'a', value = 'b' } } }`, 3},
		"own cache-control": {`
require('renders').inc()
return { headers = { ['Cache-Control'] = 'no-store' }, body = <p/> }`, 1},
	} {
		t.Run(name, func(t *testing.T) {
			ts, n := counted(t, c.src, pages.Cache(time.Minute))
			states := map[string]bool{}
			for range 3 {
				res, html := getWith(t, ts.URL)
				if name == "own cache-control" && res.Header.Get("Cache-Control") != "no-store" {
					t.Errorf("Cache-Control %q", res.Header.Get("Cache-Control"))
				}
				if name == "interactive" {
					states[stateOf(t, html)] = true
				}
			}
			if n.Load() != c.renders {
				t.Errorf("rendered %d times, want %d", n.Load(), c.renders)
			}
			if name == "interactive" && len(states) != 3 {
				t.Errorf("%d distinct states for 3 loads", len(states))
			}
		})
	}
}
