package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"sync"

	"github.com/opensecbench/opensecbench/pkg/events"
	"github.com/opensecbench/opensecbench/pkg/proxy"
)

// heldItem is one request/response paused at the proxy awaiting an operator decision.
type heldItem struct {
	id     string
	seq    int
	held   proxy.Held
	decide chan proxy.Decision
}

// interceptManager holds a project's proxy traffic and resolves holds from operator control calls. It
// implements proxy.Interceptor. All state is in-memory — held traffic is in-flight and never
// persisted; a forwarded request still becomes an exchange through the normal capture path.
type interceptManager struct {
	projectID string
	hub       *events.Hub

	mu     sync.Mutex
	reqOn  bool
	respOn bool
	seq    int
	holds  map[string]*heldItem
	done   chan struct{}
}

func newInterceptManager(projectID string, hub *events.Hub) *interceptManager {
	return &interceptManager{projectID: projectID, hub: hub, holds: map[string]*heldItem{}, done: make(chan struct{})}
}

// Enabled implements proxy.Interceptor (cheap; on the proxy hot path).
func (m *interceptManager) Enabled() (bool, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reqOn, m.respOn
}

// Hold implements proxy.Interceptor: register the held item, notify subscribers, and block until the
// operator resolves it, the client disconnects (ctx), or the proxy drains.
func (m *interceptManager) Hold(ctx context.Context, h proxy.Held) proxy.Decision {
	m.mu.Lock()
	m.seq++
	item := &heldItem{id: "h" + strconv.Itoa(m.seq), seq: m.seq, held: h, decide: make(chan proxy.Decision, 1)}
	m.holds[item.id] = item
	m.mu.Unlock()

	m.hub.Publish(events.Event{Type: "intercept.held", ProjectID: m.projectID, Payload: toHeldView(item)})

	var d proxy.Decision
	select {
	case d = <-item.decide:
	case <-ctx.Done():
		d = proxy.Decision{Drop: true}
	case <-m.done:
		d = proxy.Decision{Drop: true}
	}
	m.mu.Lock()
	delete(m.holds, item.id)
	m.mu.Unlock()
	m.hub.Publish(events.Event{Type: "intercept.resolved", ProjectID: m.projectID, Payload: map[string]string{"id": item.id}})
	return d
}

func (m *interceptManager) setEnabled(requests, responses bool) {
	m.mu.Lock()
	m.reqOn, m.respOn = requests, responses
	m.mu.Unlock()
	m.hub.Publish(events.Event{Type: "intercept", ProjectID: m.projectID, Payload: m.stateView()})
}

// resolve delivers a decision to a waiting hold. Returns false if the id is unknown or already
// resolving (its buffered channel is single-use).
func (m *interceptManager) resolve(id string, d proxy.Decision) bool {
	m.mu.Lock()
	item := m.holds[id]
	m.mu.Unlock()
	if item == nil {
		return false
	}
	select {
	case item.decide <- d:
		return true
	default:
		return false
	}
}

// drain drops every held item — called when the proxy stops or the control plane shuts down, so no
// Hold goroutine is left blocked.
func (m *interceptManager) drain() {
	m.mu.Lock()
	select {
	case <-m.done:
	default:
		close(m.done)
	}
	m.mu.Unlock()
}

// --- JSON views ---

type heldView struct {
	ID              string `json:"id"`
	Phase           string `json:"phase"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	RequestHeaders  string `json:"request_headers"`
	RequestBody     string `json:"request_body"`
	Status          int    `json:"status,omitempty"`
	ResponseHeaders string `json:"response_headers,omitempty"`
	ResponseBody    string `json:"response_body,omitempty"`
}

func toHeldView(it *heldItem) heldView {
	h := it.held
	return heldView{
		ID: it.id, Phase: string(h.Phase), Method: h.Method, URL: h.URL,
		RequestHeaders: h.RequestHeaders, RequestBody: h.RequestBody,
		Status: h.Status, ResponseHeaders: h.ResponseHeaders, ResponseBody: h.ResponseBody,
	}
}

type interceptState struct {
	Requests  bool       `json:"requests"`
	Responses bool       `json:"responses"`
	Held      []heldView `json:"held"`
}

func (m *interceptManager) stateView() interceptState {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]*heldItem, 0, len(m.holds))
	for _, it := range m.holds {
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].seq < items[j].seq })
	held := make([]heldView, 0, len(items))
	for _, it := range items {
		held = append(held, toHeldView(it))
	}
	return interceptState{Requests: m.reqOn, Responses: m.respOn, Held: held}
}

// --- HTTP handlers ---

func (s *Server) interceptManagerFor(projectID string) *interceptManager {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if lp := s.proxies[projectID]; lp != nil {
		return lp.intercept
	}
	return nil
}

// getIntercept returns the current arm state + held queue (empty when the proxy is not running).
func (s *Server) getIntercept(w http.ResponseWriter, r *http.Request) {
	m := s.interceptManagerFor(r.PathValue("id"))
	if m == nil {
		writeJSON(w, http.StatusOK, interceptState{Held: []heldView{}})
		return
	}
	writeJSON(w, http.StatusOK, m.stateView())
}

// setIntercept arms/disarms request and/or response interception. Requires a running proxy.
func (s *Server) setIntercept(w http.ResponseWriter, r *http.Request) {
	m := s.interceptManagerFor(r.PathValue("id"))
	if m == nil {
		writeErr(w, http.StatusConflict, "proxy is not running")
		return
	}
	var req struct {
		Requests  bool `json:"requests"`
		Responses bool `json:"responses"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	m.setEnabled(req.Requests, req.Responses)
	s.record(r.Context(), actorOf(r), "intercept.arm", r.PathValue("id"), map[string]bool{"requests": req.Requests, "responses": req.Responses})
	writeJSON(w, http.StatusOK, m.stateView())
}

// resolveIntercept forwards (optionally edited) or drops one held item.
func (s *Server) resolveIntercept(w http.ResponseWriter, r *http.Request) {
	m := s.interceptManagerFor(r.PathValue("id"))
	if m == nil {
		writeErr(w, http.StatusConflict, "proxy is not running")
		return
	}
	var req struct {
		Action          string `json:"action"` // "forward" | "drop"
		Method          string `json:"method"`
		URL             string `json:"url"`
		RequestHeaders  string `json:"request_headers"`
		RequestBody     string `json:"request_body"`
		Status          int    `json:"status"`
		ResponseHeaders string `json:"response_headers"`
		ResponseBody    string `json:"response_body"`
	}
	if !decodeJSON(w, r, &req) {
		return
	}
	d := proxy.Decision{Drop: req.Action == "drop"}
	if !d.Drop {
		d.Method, d.URL = req.Method, req.URL
		d.RequestHeaders, d.RequestBody = req.RequestHeaders, req.RequestBody
		d.Status, d.ResponseHeaders, d.ResponseBody = req.Status, req.ResponseHeaders, req.ResponseBody
	}
	if !m.resolve(r.PathValue("holdId"), d) {
		writeErr(w, http.StatusNotFound, "no such held item (already resolved?)")
		return
	}
	action := "forward"
	if d.Drop {
		action = "drop"
	}
	s.record(r.Context(), actorOf(r), "intercept."+action, r.PathValue("holdId"), map[string]string{"url": req.URL})
	writeJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}
