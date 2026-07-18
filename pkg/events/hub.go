// Package events is a small in-process publish/subscribe hub for control-plane domain events
// (captured HTTP exchanges today; tasks, approvals, and Analyst tokens later). Clients receive a
// project-filtered live stream over Server-Sent Events (see pkg/api). The proxy and Replay publish
// here instead of clients polling the exchange list.
//
// Publishing never blocks: a subscriber that can't keep up simply drops events. That is safe because
// every client resynchronizes with a full fetch on (re)connect, so a slow or stalled reader can never
// stall the proxy or any other publisher — availability of the core path beats delivery of every tick.
package events

import "sync"

// Event is one domain event. ProjectID is the routing key; "" means global (delivered to everyone).
type Event struct {
	Type      string `json:"type"`
	ProjectID string `json:"project_id,omitempty"`
	Payload   any    `json:"payload,omitempty"`
}

// buffer bounds each subscriber's backlog before events are dropped for that subscriber.
const buffer = 64

// Hub fans events out to subscribers, filtered by project.
type Hub struct {
	mu     sync.Mutex
	nextID int
	subs   map[int]subscriber
}

type subscriber struct {
	projectID string // "" = every project
	ch        chan Event
}

// NewHub returns an empty hub ready for use.
func NewHub() *Hub {
	return &Hub{subs: make(map[int]subscriber)}
}

// Subscribe returns a channel of events for projectID plus global events, and an unsubscribe func
// that is safe to call exactly once. Pass "" to receive every event.
func (h *Hub) Subscribe(projectID string) (<-chan Event, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.nextID
	h.nextID++
	ch := make(chan Event, buffer)
	h.subs[id] = subscriber{projectID: projectID, ch: ch}

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if s, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(s.ch)
			}
		})
	}
	return ch, unsubscribe
}

// Publish delivers e to every matching subscriber without blocking. Subscribe and Publish share the
// mutex, so a channel is never closed concurrently with a send; a full buffer drops the event.
func (h *Hub) Publish(e Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.subs {
		// A project subscriber gets its own project's events and global ("") events; a global
		// subscriber ("") gets everything.
		if s.projectID != "" && e.ProjectID != "" && s.projectID != e.ProjectID {
			continue
		}
		select {
		case s.ch <- e:
		default: // slow subscriber; drop and let it resync on reconnect
		}
	}
}
