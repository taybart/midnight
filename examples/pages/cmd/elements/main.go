// The todo example done client side, on net/http's ServeMux: the page
// (client.mn) is rendered once, and custom elements (todo/ui/elements) use
// the store's JSON api (todo.API) in the browser. Compare with cmd/std, where
// the same page's actions run on the server.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/taybart/midnight/examples/pages/todo"
	"github.com/taybart/midnight/pages"
)

func routes(eng *pages.Engine, store *todo.Store, uiDir string) http.Handler {
	mux := http.NewServeMux()
	// GET only: the page has no actions
	mux.Handle("GET /todos/{$}", http.StripPrefix("/todos", todo.ClientPage(eng, store, uiDir, "net/http")))
	mux.Handle("/todos/api/", http.StripPrefix("/todos", todo.API(store)))
	mux.Handle("GET /todos/tb.css", todo.Styles(uiDir))
	// the elements' js, which the page imports relatively (./elements.js)
	mux.Handle("GET /todos/elements.js", http.StripPrefix("/todos", todo.Scripts(uiDir)))
	mux.Handle("GET /todos/elements/", http.StripPrefix("/todos", todo.Scripts(uiDir)))
	mux.Handle("/{$}", http.RedirectHandler("/todos/", http.StatusFound))
	return mux
}

func main() {
	addr := flag.String("addr", "localhost:8092", "address to listen on")
	ui := flag.String("ui", "", "serve the page's files from this directory (ex. todo/ui, to edit them live) instead of the embedded ones")
	flag.Parse()

	eng := pages.New(pages.Options{})
	defer eng.Close()
	log.Printf("http://%s/todos/", *addr)
	log.Fatal(http.ListenAndServe(*addr, routes(eng, todo.NewStore(), *ui)))
}
