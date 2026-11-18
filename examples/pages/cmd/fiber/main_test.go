package main

import (
	"net"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/taybart/midnight/examples/pages/todo"
	"github.com/taybart/midnight/examples/pages/todo/todotest"
	"github.com/taybart/midnight/pages"
)

func TestTodos(t *testing.T) {
	eng := pages.New(pages.Options{})
	defer eng.Close()
	// the files on disk
	// a fiber app isn't an http.Handler (no httptest.NewServer): serve it on
	// a listener
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	app := routes(eng, todo.NewStore(), "../../todo/ui")
	go app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true})
	defer app.Shutdown()
	todotest.Run(t, "http://"+ln.Addr().String()+"/todos/", "fiber")
}
