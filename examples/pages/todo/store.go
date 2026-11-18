// Package todo is the example's app: a todo list kept in memory, and the
// page that shows it.
package todo

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

type Todo struct {
	ID      int       `json:"id"`
	Title   string    `json:"title"`
	Done    bool      `json:"done"`
	Created time.Time `json:"created"`
}

// Store is a todo list, shared by every visitor.
type Store struct {
	mu    sync.Mutex
	todos []Todo
	next  int
}

func NewStore() *Store { return &Store{next: 1} }

func (s *Store) List(context.Context) []Todo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Todo(nil), s.todos...)
}

func (s *Store) Add(_ context.Context, title string) (Todo, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Todo{}, errors.New("a todo needs a title")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := Todo{ID: s.next, Title: title, Created: time.Now()}
	s.next++
	s.todos = append(s.todos, t)
	return t, nil
}

func (s *Store) Toggle(_ context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.todos {
		if s.todos[i].ID == id {
			s.todos[i].Done = !s.todos[i].Done
			return nil
		}
	}
	return errors.New("no such todo")
}

func (s *Store) Remove(_ context.Context, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.todos {
		if s.todos[i].ID == id {
			s.todos = append(s.todos[:i], s.todos[i+1:]...)
			return nil
		}
	}
	return errors.New("no such todo")
}
