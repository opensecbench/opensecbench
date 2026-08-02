package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/opensecbench/opensecbench/pkg/events"
)

// Publish emits a domain event to the live bus (ADR-0063). It is the seam other subsystems that lack a
// direct hub reference — notably the task engine — use to stream task completions and new findings, so
// every client reacts without polling. projectID "" routes globally.
func (s *Server) Publish(projectID, eventType string, payload any) {
	if s.events == nil {
		return
	}
	s.events.Publish(events.Event{Type: eventType, ProjectID: projectID, Payload: payload})
}

// projectEvents streams a project's live domain events (captured exchanges, proxy start/stop, …) as
// Server-Sent Events, so clients react instead of polling. The event hub (pkg/events) is the source;
// the proxy and Replay publish to it. A client resyncs with a normal fetch on connect, so dropped
// events during a slow spell are self-healing.
func (s *Server) projectEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // don't let any intermediary buffer the stream

	ch, unsubscribe := s.events.Subscribe(r.PathValue("id"))
	defer unsubscribe()

	_, _ = fmt.Fprint(w, ": connected\n\n")
	flusher.Flush()

	// Heartbeat so idle connections stay open and dead ones are noticed promptly.
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		case ev, open := <-ch:
			if !open {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, payload)
			flusher.Flush()
		}
	}
}
