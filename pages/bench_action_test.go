package pages_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/taybart/midnight/pages"
)

// An action on the testdata app (dispatch + re-render of what changed), for
// comparing against the cost of Funcs calls.
func BenchmarkAction(b *testing.B) {
	eng := pages.New(pages.Options{})
	defer eng.Close()
	h := eng.File("testdata/app.mn", pages.Props(func(r *http.Request) (any, error) {
		return map[string]any{"name": "bench"}, nil
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	html := rec.Body.String()
	state := regexp.MustCompile(`["']` + pages.StateHeader + `["']:\s*["']([^"']+)`).FindStringSubmatch(html)[1]
	id := actionID.FindStringSubmatch(html)[1]
	body := `{"id":"` + id + `"}`
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set(pages.StateHeader, state)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		io.Copy(io.Discard, rec.Body)
	}
}
