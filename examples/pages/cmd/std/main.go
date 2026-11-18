// The todo example on net/http's ServeMux, mounted under /todos/ with
// http.StripPrefix (the page's actions POST to the url the browser sees, so
// prefixes just work).
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
	// a pattern without a method: GET renders the page, POST runs its actions
	mux.Handle("/todos/", http.StripPrefix("/todos", todo.Page(eng, store, uiDir, "net/http")))
	mux.Handle("GET /todos/tb.css", todo.Styles(uiDir))
	mux.Handle("/{$}", http.RedirectHandler("/todos/", http.StatusFound))
	return mux
}

func main() {
	addr := flag.String("addr", "localhost:8090", "address to listen on")
	ui := flag.String("ui", "", "serve the page's files from this directory (ex. todo/ui, to edit them live) instead of the embedded ones")
	flag.Parse()

	eng := pages.New(pages.Options{})
	defer eng.Close()
	log.Printf("http://%s/todos/", *addr)
	log.Fatal(http.ListenAndServe(*addr, routes(eng, todo.NewStore(), *ui)))
}
