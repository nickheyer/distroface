package mirror

import (
	"sync"
	"time"
)

type Phase int

const (
	PhaseStarted Phase = iota + 1
	PhaseCompleted
	PhaseFailed
)

// Event is one repo entering or leaving a sync
type Event struct {
	// Either image or artifact
	Kind      string
	RepoID    string
	Namespace string
	Name      string
	// Viewer filtering happens at the rpc layer
	Private bool
	OwnerID string
	Phase   Phase
	Err     string
	At      time.Time
}

// Fan out channel registry, slow subscribers drop events
type hub struct {
	mu   sync.Mutex
	subs map[chan Event]struct{}
}

func newHub() *hub {
	return &hub{subs: make(map[chan Event]struct{})}
}

func (h *hub) subscribe() (<-chan Event, func()) {
	ch := make(chan Event, 32)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	cancel := func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
	return ch, cancel
}

func (h *hub) publish(ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe delivers sync events until cancel is called
func (m *Monitor) Subscribe() (<-chan Event, func()) {
	return m.events.subscribe()
}

// Active snapshots every sync running right now
func (m *Monitor) Active() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, 0, len(m.activeSyncs))
	for _, ev := range m.activeSyncs {
		out = append(out, ev)
	}
	return out
}

// IsSyncing reports whether the keyed repo has a sync in flight
func (m *Monitor) IsSyncing(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.activeSyncs[key]
	return ok
}

func (m *Monitor) beginSync(key string, ev Event) {
	ev.Phase = PhaseStarted
	ev.At = time.Now().UTC()
	m.mu.Lock()
	m.activeSyncs[key] = ev
	m.mu.Unlock()
	m.events.publish(ev)
}

func (m *Monitor) endSync(key string, ev Event, syncErr error) {
	ev.Phase = PhaseCompleted
	ev.At = time.Now().UTC()
	if syncErr != nil {
		ev.Phase = PhaseFailed
		ev.Err = truncate(syncErr.Error(), 1000)
	}
	m.mu.Lock()
	delete(m.activeSyncs, key)
	m.mu.Unlock()
	m.events.publish(ev)
}
