package todo

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// API is the store as a JSON api, for the client side page (client.mn, its
// <todo-list> element). Each change responds with the whole list; errors are
// {"error": "..."}.
//
//	GET    /api/todos             the list
//	POST   /api/todos             add one ({"title": "..."})
//	POST   /api/todos/{id}/toggle toggle one
//	DELETE /api/todos/{id}        remove one
func API(store *Store) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/todos", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, store.List(r.Context()))
	})
	mux.HandleFunc("POST /api/todos", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "expected {\"title\": \"...\"}")
			return
		}
		if _, err := store.Add(r.Context(), body.Title); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, store.List(r.Context()))
	})
	byID := func(fn func(*http.Request, int) error) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, err := strconv.Atoi(r.PathValue("id"))
			if err == nil {
				err = fn(r, id)
			}
			if err != nil {
				writeError(w, http.StatusNotFound, "no such todo")
				return
			}
			writeJSON(w, http.StatusOK, store.List(r.Context()))
		}
	}
	mux.HandleFunc("POST /api/todos/{id}/toggle", byID(func(r *http.Request, id int) error {
		return store.Toggle(r.Context(), id)
	}))
	mux.HandleFunc("DELETE /api/todos/{id}", byID(func(r *http.Request, id int) error {
		return store.Remove(r.Context(), id)
	}))
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	if todos, ok := v.([]Todo); ok && todos == nil {
		v = []Todo{} // [], not null
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
