package todo

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/taybart/midnight/pages"
)

// the page's files (app.mn and its parts, tb.css), built into the binary
//
//go:embed ui
var embedded embed.FS

// UI returns the page's files: the embedded ones, or with a uiDir, the ones in
// that directory (for editing them while it runs).
func UI(uiDir string) fs.FS {
	if uiDir != "" {
		return os.DirFS(uiDir)
	}
	ui, _ := fs.Sub(embedded, "ui")
	return ui
}

// Styles serves tb.css (the page's base styles, next to it in UI(uiDir)). The
// page links it relatively ("tb.css"), so it's served next to the page's
// route.
func Styles(uiDir string) http.Handler {
	ui := UI(uiDir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, ui, "tb.css")
	})
}

// Scripts serves the custom elements' js (elements.js, elements/*.js, next
// to the page in UI(uiDir)) for the client side page. Only .js: not the
// page's sources.
func Scripts(uiDir string) http.Handler {
	ui := UI(uiDir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if path.Ext(name) != ".js" || !fs.ValidPath(name) {
			http.NotFound(w, r)
			return
		}
		http.ServeFileFS(w, r, ui, name)
	})
}

// ClientPage is the todo page done client side (client.mn): rendered once,
// with custom elements that use the JSON api (see API) in the browser.
func ClientPage(eng *pages.Engine, store *Store, uiDir, router string) http.Handler {
	opts := []pages.Option{
		pages.Props(func(r *http.Request) (any, error) {
			return map[string]any{"router": router}, nil
		}),
		// only for the first render: changes go through the api
		pages.Funcs("app.todos", map[string]any{"list": store.List}),
	}
	if uiDir != "" {
		return eng.File(filepath.Join(uiDir, "client.mn"), opts...)
	}
	return eng.FS(UI(""), "client.mn", opts...)
}

// Page is the todo page (app.mn), with the store's methods as the `app.todos`
// module. router is shown on the page.
//
// Without a uiDir, it's served from the embedded files (eng.FS: compiled once,
// nothing read from disk). With one, from that directory (eng.File: changes
// show up on the next page load).
func Page(eng *pages.Engine, store *Store, uiDir, router string) http.Handler {
	opts := []pages.Option{
		pages.Props(func(r *http.Request) (any, error) {
			return map[string]any{"router": router}, nil
		}),
		// plain Go methods: the context is the request being handled, args
		// and results are converted (Todo becomes a table by its json tags),
		// and errors are lua errors
		pages.Funcs("app.todos", map[string]any{
			"list":   store.List,
			"add":    store.Add,
			"toggle": store.Toggle,
			"remove": store.Remove,
		}),
	}
	if uiDir != "" {
		return eng.File(filepath.Join(uiDir, "app.mn"), opts...)
	}
	return eng.FS(UI(""), "app.mn", opts...)
}
