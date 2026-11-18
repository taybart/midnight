package pages

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

func stateRequest(id string) *http.Request {
	req, _ := http.NewRequest("GET", "/", nil)
	if id != "" {
		req.Header.Set(StateHeader, id)
	}
	return req
}

// The least recently used state is evicted to make room, and unused states
// expire.
func TestStateStoreLimits(t *testing.T) {
	st := NewStateStore(2, time.Hour)
	ids := []string{}
	for range 3 {
		id, s, _, err := st.Acquire(stateRequest(""))
		if err != nil {
			t.Fatal(err)
		}
		st.Release(s)
		ids = append(ids, id)
		time.Sleep(time.Millisecond) // distinct lastUsed
	}
	if n := st.Len(); n != 2 {
		t.Errorf("expected 2 states, got %d", n)
	}
	var gone ErrStateGone
	if _, _, _, err := st.Acquire(stateRequest(ids[0])); !errors.As(err, &gone) {
		t.Errorf("oldest state: expected gone, got %v", err)
	}

	// using a state makes it the most recent
	_, s, _, err := st.Acquire(stateRequest(ids[1]))
	if err != nil {
		t.Fatal(err)
	}
	st.Release(s)
	_, s, _, _ = st.Acquire(stateRequest(""))
	st.Release(s)
	if _, _, _, err := st.Acquire(stateRequest(ids[2])); !errors.As(err, &gone) {
		t.Errorf("least recently used: expected gone, got %v", err)
	}
	if _, s, _, err := st.Acquire(stateRequest(ids[1])); err != nil {
		t.Errorf("recently used: %v", err)
	} else {
		st.Release(s)
	}

	// states in use aren't evicted (the store goes over instead)
	_, busy, _, _ := st.Acquire(stateRequest(ids[1]))
	_, s, _, _ = st.Acquire(stateRequest(""))
	st.Release(s)
	_, s, _, _ = st.Acquire(stateRequest(""))
	st.Release(s)
	if busy.closed {
		t.Error("evicted a state in use")
	}
	st.Release(busy)

	// expiry
	st = NewStateStore(10, 10*time.Millisecond)
	id, s, _, _ := st.Acquire(stateRequest(""))
	st.Release(s)
	time.Sleep(20 * time.Millisecond)
	if _, _, _, err := st.Acquire(stateRequest(id)); !errors.As(err, &gone) {
		t.Errorf("expired: expected gone, got %v", err)
	}
}

// Discarded states are closed on release.
func TestStateStoreDiscard(t *testing.T) {
	st := NewStateStore(0, 0)
	id, s, _, _ := st.Acquire(stateRequest(""))
	st.Discard(id, s)
	st.Release(s)
	if !s.closed || st.Len() != 0 {
		t.Errorf("closed %v, states %d", s.closed, st.Len())
	}
}
