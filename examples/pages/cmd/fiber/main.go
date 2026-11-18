// The todo example on fiber (v3), in a /todos group. Fiber runs on fasthttp,
// not net/http, so the page handler (an http.Handler) goes through fiber's
// adaptor, which converts each request and response.
package main

import (
	"flag"
	"log"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
	"github.com/taybart/midnight/examples/pages/todo"
	"github.com/taybart/midnight/pages"
)

func routes(eng *pages.Engine, store *todo.Store, uiDir string) *fiber.App {
	app := fiber.New()
	page := adaptor.HTTPHandler(todo.Page(eng, store, uiDir, "fiber"))
	todos := app.Group("/todos")
	// GET renders the page, POST runs its actions
	todos.Get("/", page)
	todos.Post("/", page)
	// static files the fiber way (no adaptor needed)
	ui := todo.UI(uiDir)
	todos.Get("/tb.css", func(c fiber.Ctx) error { return c.SendFile("tb.css", fiber.SendFile{FS: ui}) })
	app.Get("/", func(c fiber.Ctx) error { return c.Redirect().To("/todos/") })
	return app
}

func main() {
	addr := flag.String("addr", "localhost:8091", "address to listen on")
	ui := flag.String("ui", "", "serve the page's files from this directory (ex. todo/ui, to edit them live) instead of the embedded ones")
	flag.Parse()

	eng := pages.New(pages.Options{})
	defer eng.Close()
	log.Printf("http://%s/todos/", *addr)
	log.Fatal(routes(eng, todo.NewStore(), *ui).Listen(*addr, fiber.ListenConfig{DisableStartupMessage: true}))
}
