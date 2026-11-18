// Package todotest drives the todo page like its client script does, for
// testing it behind different routers.
package todotest

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/taybart/midnight/pages"
)

var stateRe = regexp.MustCompile(`["']` + pages.StateHeader + `["']:\s*["']([^"']+)`)

// action returns the id of the first `on<event>` action in html.
func action(t *testing.T, html, event string) string {
	t.Helper()
	m := regexp.MustCompile(`on` + event + `="midnight\.rpc\('([^']+)'`).FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no %s action in\n%s", event, html)
	}
	return m[1]
}

// Run loads the page at url (the router's mount point, ex.
// http://host/todos/) and adds, toggles, and removes a todo, with its actions
// POSTed to the same url (what the client does).
func Run(t *testing.T, url, router string) {
	t.Helper()
	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	html := string(b)
	if res.StatusCode != 200 || !strings.Contains(html, "served with "+router) || !strings.Contains(html, "nothing to do") || !strings.Contains(html, "<span>0 left</span>") {
		t.Fatalf("page: %d\n%s", res.StatusCode, html)
	}
	// the page links its styles relatively, so they're next to it
	css, err := http.Get(url + "tb.css")
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := io.ReadAll(css.Body)
	css.Body.Close()
	if css.StatusCode != 200 || !strings.Contains(string(cb), "--bg-color") {
		t.Errorf("tb.css: %d", css.StatusCode)
	}

	m := stateRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatalf("no state in\n%s", html)
	}
	state := m[1]

	act := func(id string, data map[string]any) string {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"id": id, "data": data})
		req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(string(body)))
		req.Header.Set(pages.StateHeader, state)
		req.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("action %s: %d %s", id, res.StatusCode, b)
		}
		return string(b)
	}
	submit := action(t, html, "submit")

	// a Go error, caught by the page (pcall) and shown
	out := act(submit, map[string]any{"type": "submit", "form": map[string]any{"title": "  "}})
	if !strings.Contains(out, "a todo needs a title") {
		t.Errorf("empty title: %s", out)
	}

	out = act(submit, map[string]any{"type": "submit", "form": map[string]any{"title": "milk"}})
	if !regexp.MustCompile(`<span class="title-[^"]+">milk</span>`).MatchString(out) || !strings.Contains(out, "<span>1 left</span>") || strings.Contains(out, "needs a title") {
		t.Fatalf("add: %s", out)
	}

	out = act(action(t, out, "change"), map[string]any{"type": "change", "checked": true})
	if !regexp.MustCompile(`checked[^>]*>\s*<span class="title-[^" ]+ done-[^"]+">milk</span>`).MatchString(out) || !strings.Contains(out, "<span>1 done</span>") {
		t.Errorf("toggle: %s", out)
	}

	out = act(action(t, out, "click"), map[string]any{"type": "click"})
	if strings.Contains(out, "milk") || !strings.Contains(out, "nothing to do") {
		t.Errorf("remove: %s", out)
	}
}
