package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/taybart/midnight/examples/pages/todo"
	"github.com/taybart/midnight/pages"
)

func TestTodos(t *testing.T) {
	eng := pages.New(pages.Options{})
	defer eng.Close()
	ts := httptest.NewServer(routes(eng, todo.NewStore(), ""))
	defer ts.Close()
	url := ts.URL + "/todos/"

	get := func(path string) (int, string, string) {
		t.Helper()
		res, err := http.Get(url + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, res.Header.Get("Content-Type"), string(b)
	}

	// the page: rendered once, with the elements' module and no actions
	status, _, html := get("")
	for _, want := range []string{
		"served with net/http + custom elements",
		`<script type="module">import * as m from './elements.js'`,
		"r('todo-list', 'TodoList');",
		`<todo-list api="api/todos" done-class="done-`,
		"<template><li ",
		"<span data-left>0</span>",
	} {
		if !strings.Contains(html, want) {
			t.Fatalf("page (%d) has no %q:\n%s", status, want, html)
		}
	}
	// (and so no marks for updates either)
	if strings.Contains(html, "midnight.rpc") || strings.Contains(html, pages.StateHeader) || strings.Contains(html, "data-midnight=") {
		t.Errorf("page has actions:\n%s", html)
	}

	// the elements' js, but not the page's sources
	for _, path := range []string{"elements.js", "elements/todo-list.js", "elements/theme-toggle.js"} {
		if status, ct, _ := get(path); status != 200 || !strings.HasPrefix(ct, "text/javascript") {
			t.Errorf("%s: %d %s", path, status, ct)
		}
	}
	if status, _, _ := get("client.mn"); status != 404 {
		t.Errorf("client.mn: %d", status)
	}

	// the api, as the <todo-list> element calls it
	call := func(method, path, body string, wantStatus int) string {
		t.Helper()
		req, _ := http.NewRequest(method, url+"api/todos"+path, strings.NewReader(body))
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != wantStatus || !json.Valid(b) {
			t.Fatalf("%s %s: %d %s", method, path, res.StatusCode, b)
		}
		return string(b)
	}
	if out := call("POST", "", `{"title": "  "}`, 400); !strings.Contains(out, "a todo needs a title") {
		t.Errorf("empty title: %s", out)
	}
	if out := call("POST", "", `{"title": "milk"}`, 200); !strings.Contains(out, `"id":1,"title":"milk","done":false`) {
		t.Errorf("add: %s", out)
	}
	if out := call("POST", "/1/toggle", "", 200); !strings.Contains(out, `"done":true`) {
		t.Errorf("toggle: %s", out)
	}
	call("POST", "/2/toggle", "", 404)
	// the next render has it
	if _, _, html := get(""); !strings.Contains(html, ">milk</span>") || !strings.Contains(html, "<span data-done>1</span>") {
		t.Errorf("page after add:\n%s", html)
	}
	if out := call("DELETE", "/1", "", 200); strings.TrimSpace(out) != "[]" {
		t.Errorf("remove: %s", out)
	}
}
