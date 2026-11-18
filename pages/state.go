package pages

import (
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	lua "github.com/taybart/midnight"
)

// A StateStore keeps lua states between requests (for pages, and rest's
// `stateful = true` handlers), so globals, required modules, and closures
// survive. A request without a state id gets a new state; requests that send its id
// back in the StateHeader reuse it (one request at a time).
//
// States cost memory (roughly 0.5-2MB each, depending on what's loaded), so
// they're limited: unused states are closed after the store's ttl, and when
// there are max states, the least recently used one is closed to make room. A
// request for a closed (or unknown) state gets ErrStateGone (a 410).

const (
	StateHeader      = "X-Midnight-State"
	DefaultMaxStates = 1000
	DefaultStateTTL  = 30 * time.Minute
)

// State is a kept lua state. L is only for the request that acquired it, until
// it's released.
type State struct {
	mu       sync.Mutex // held while a request uses the state
	L        *lua.LState
	lastUsed time.Time // guarded by StateStore.mu
	// set (by the request holding mu) to close the state on release
	discarded bool
	// set (holding mu) once closed, for requests that were waiting for it
	closed bool
}

// StateStore keeps lua states by id (see NewStateStore).
type StateStore struct {
	mu     sync.Mutex
	states map[string]*State
	max    int
	ttl    time.Duration
}

// NewStateStore makes a store of at most max states (DefaultMaxStates if <= 0),
// closed once unused for ttl (DefaultStateTTL if <= 0).
func NewStateStore(max int, ttl time.Duration) *StateStore {
	if max <= 0 {
		max = DefaultMaxStates
	}
	if ttl <= 0 {
		ttl = DefaultStateTTL
	}
	return &StateStore{states: map[string]*State{}, max: max, ttl: ttl}
}

func newStateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ErrStateGone is returned for a state id that doesn't exist (anymore), ex.
// after a restart, or once it expired or was evicted.
type ErrStateGone struct{ id string }

func (e ErrStateGone) Error() string { return "state " + e.id + " is gone" }

// closeIdle closes and removes a state that isn't in use (the store is
// locked).
func (st *StateStore) closeIdle(id string, s *State) bool {
	if !s.mu.TryLock() {
		return false
	}
	s.L.Close()
	s.closed = true
	delete(st.states, id)
	s.mu.Unlock()
	return true
}

// Acquire returns the request's lua state (by its StateHeader, or a new one if
// it has none), locked until Release. The bool is whether the state is new
// (and needs setting up).
func (st *StateStore) Acquire(req *http.Request) (string, *State, bool, error) {
	if id := req.Header.Get(StateHeader); id != "" {
		s, err := st.Get(id)
		return id, s, false, err
	}
	id, s := st.New()
	return id, s, true, nil
}

// expire closes states unused for longer than the ttl (the store is locked).
func (st *StateStore) expire(now time.Time) {
	for id, s := range st.states {
		if now.Sub(s.lastUsed) > st.ttl {
			st.closeIdle(id, s)
		}
	}
}

// Get returns the state with this id, locked until Release, or ErrStateGone.
func (st *StateStore) Get(id string) (*State, error) {
	st.mu.Lock()
	now := time.Now()
	st.expire(now)
	s, ok := st.states[id]
	if !ok {
		st.mu.Unlock()
		return nil, ErrStateGone{id}
	}
	s.lastUsed = now
	st.mu.Unlock()
	// outside of the store lock, so requests to other states don't wait
	s.mu.Lock()
	if s.closed { // closed while waiting
		s.mu.Unlock()
		return nil, ErrStateGone{id}
	}
	return s, nil
}

// New returns a new state and its id, locked until Release.
func (st *StateStore) New() (string, *State) {
	st.mu.Lock()
	now := time.Now()
	st.expire(now)
	// make room, least recently used first (states in use are skipped, so
	// under a lot of concurrent requests this can briefly go over)
	for len(st.states) >= st.max {
		oldestID, oldest := "", (*State)(nil)
		for id, s := range st.states {
			if oldest == nil || s.lastUsed.Before(oldest.lastUsed) {
				oldestID, oldest = id, s
			}
		}
		if !st.closeIdle(oldestID, oldest) {
			break
		}
	}

	id := newStateID()
	s := &State{L: lua.NewState(), lastUsed: now}
	s.mu.Lock()
	st.states[id] = s
	st.mu.Unlock()
	return id, s
}

// Discard removes a state that's in use (by the caller); it's closed on
// release.
func (st *StateStore) Discard(id string, s *State) {
	st.mu.Lock()
	delete(st.states, id)
	st.mu.Unlock()
	s.discarded = true
}

// Release unlocks a state acquired with Acquire (closing it if discarded).
func (st *StateStore) Release(s *State) {
	if s.discarded {
		s.L.Close()
		s.closed = true
	} else {
		st.mu.Lock()
		s.lastUsed = time.Now()
		st.mu.Unlock()
	}
	s.mu.Unlock()
}

func (st *StateStore) Len() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.states)
}

// Close closes every state that isn't in use, ex. on shutdown.
func (st *StateStore) Close() {
	st.mu.Lock()
	defer st.mu.Unlock()
	for id, s := range st.states {
		st.closeIdle(id, s)
	}
}
