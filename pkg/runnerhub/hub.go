// Package runnerhub brokers work between the control plane and connected remote runners (ADR-0024). A
// runner dials home and holds open a downstream stream; the hub pushes task dispatches (and cancels)
// down it and matches the runner's posted results back to the waiting caller. It is the in-process
// rendezvous between the durable task queue (which produces RunSpecs) and the runner protocol.
package runnerhub

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/runnertunnel"
)

// TunnelForward is the OPEN-frame metadata for a proxy request forwarded through a runner over the
// streaming tunnel (ADR-0026): the request line + headers; the body streams as the stream's payload.
type TunnelForward struct {
	Method        string      `json:"method"`
	URL           string      `json:"url"`
	Header        http.Header `json:"header"`
	ContentLength int64       `json:"content_length"`
	Insecure      bool        `json:"insecure"`
}

// Dispatch messages flow down a runner's stream.
const (
	KindRun    = "run"
	KindCancel = "cancel"
	KindHTTP   = "http" // perform an outbound HTTP request from the runner's vantage (ADR-0025)
)

// Dispatch is one message on a runner's downstream stream: run a capability task, cancel one, or perform
// an outbound HTTP request (Replay egress via runner).
type Dispatch struct {
	Kind   string         `json:"kind"`
	TaskID string         `json:"task_id,omitempty"`
	Spec   runner.RunSpec `json:"spec,omitempty"`
	HTTP   *HTTPRequest   `json:"http,omitempty"`
}

// HTTPRequest is an outbound request the runner should perform on the control plane's behalf (ADR-0025).
// Headers are "Key: value" lines, matching replay.Request.
type HTTPRequest struct {
	ID      string `json:"id"`
	Method  string `json:"method"`
	URL     string `json:"url"`
	Headers string `json:"headers"`
	Body    string `json:"body"`
}

// HTTPResult is the runner's captured response to an HTTPRequest.
type HTTPResult struct {
	ID         string `json:"id"`
	Status     int    `json:"status"`
	Headers    string `json:"headers"`
	Body       string `json:"body"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"` // set when the request could not be carried out
}

var (
	// ErrRunnerOffline means the target runner has no live stream connected.
	ErrRunnerOffline = errors.New("runnerhub: runner offline")
	// ErrRunnerBusy means the runner's stream is not draining dispatches.
	ErrRunnerBusy = errors.New("runnerhub: runner not accepting dispatch")
)

const dispatchTimeout = 5 * time.Second

type conn struct {
	ch   chan Dispatch
	done chan struct{}
}

// httpWaiter is a pending HTTP request keyed by request id — the owning runner and the result channel.
type httpWaiter struct {
	runnerID string
	ch       chan HTTPResult
}

// Hub is the control-plane-side broker. Safe for concurrent use.
type Hub struct {
	mu          sync.Mutex
	conns       map[string]*conn                 // runnerID -> live SSE dispatch stream
	pending     map[string]chan runner.Result    // taskID -> capability-result waiter
	pendingHTTP map[string]httpWaiter            // requestID -> HTTP-result waiter
	tunnels     map[string]*runnertunnel.Session // runnerID -> live streaming tunnel (ADR-0026)
	Replay      *ReplayGuard                     // anti-replay for signed runner requests
}

// New builds an empty hub.
func New() *Hub {
	return &Hub{
		conns:       map[string]*conn{},
		pending:     map[string]chan runner.Result{},
		pendingHTTP: map[string]httpWaiter{},
		tunnels:     map[string]*runnertunnel.Session{},
		Replay:      NewReplayGuard(),
	}
}

// RegisterTunnel records a runner's live streaming tunnel, replacing (and closing) any prior one.
func (h *Hub) RegisterTunnel(runnerID string, sess *runnertunnel.Session) {
	h.mu.Lock()
	old := h.tunnels[runnerID]
	h.tunnels[runnerID] = sess
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// RemoveTunnel detaches a runner's tunnel if it is still the registered one.
func (h *Hub) RemoveTunnel(runnerID string, sess *runnertunnel.Session) {
	h.mu.Lock()
	if h.tunnels[runnerID] == sess {
		delete(h.tunnels, runnerID)
	}
	h.mu.Unlock()
}

// TunnelFor returns a runner's live streaming tunnel, if connected.
func (h *Hub) TunnelFor(runnerID string) (*runnertunnel.Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.tunnels[runnerID]
	return s, ok
}

// Subscription is a connected runner's downstream stream. The SSE handler reads Ch until Done closes (the
// runner was evicted by a newer connection) or the request ends, then calls Close.
type Subscription struct {
	Ch    <-chan Dispatch
	Done  <-chan struct{}
	close func()
}

// Close detaches the runner's stream (idempotent).
func (s *Subscription) Close() { s.close() }

// Register attaches a connected runner's stream. A second connection for the same runner evicts the
// first (its Done closes).
func (h *Hub) Register(runnerID string) *Subscription {
	c := &conn{ch: make(chan Dispatch, 16), done: make(chan struct{})}
	h.mu.Lock()
	if old := h.conns[runnerID]; old != nil {
		close(old.done) // evict the stale connection
	}
	h.conns[runnerID] = c
	h.mu.Unlock()

	var once sync.Once
	closeFn := func() {
		once.Do(func() {
			h.mu.Lock()
			if h.conns[runnerID] == c {
				delete(h.conns, runnerID)
				close(c.done)
			}
			h.mu.Unlock()
		})
	}
	return &Subscription{Ch: c.ch, Done: c.done, close: closeFn}
}

// Online reports whether a runner has a live stream.
func (h *Hub) Online(runnerID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.conns[runnerID] != nil
}

// Dispatch pushes a task to a connected runner and returns a channel that will receive its result. It
// errors if the runner is offline or its stream is not draining.
func (h *Hub) Dispatch(runnerID, taskID string, spec runner.RunSpec) (<-chan runner.Result, error) {
	h.mu.Lock()
	c := h.conns[runnerID]
	if c == nil {
		h.mu.Unlock()
		return nil, ErrRunnerOffline
	}
	resCh := make(chan runner.Result, 1)
	h.pending[taskID] = resCh
	h.mu.Unlock()

	select {
	case c.ch <- Dispatch{Kind: KindRun, TaskID: taskID, Spec: spec}:
		return resCh, nil
	case <-c.done:
		h.forget(taskID)
		return nil, ErrRunnerOffline
	case <-time.After(dispatchTimeout):
		h.forget(taskID)
		return nil, ErrRunnerBusy
	}
}

// Deliver hands a runner's result to the waiting Dispatch caller. Returns false if nothing was waiting
// (e.g. the caller already gave up, or the task id is unknown).
func (h *Hub) Deliver(taskID string, res runner.Result) bool {
	h.mu.Lock()
	ch := h.pending[taskID]
	delete(h.pending, taskID)
	h.mu.Unlock()
	if ch == nil {
		return false
	}
	ch <- res // buffered (size 1), never blocks
	return true
}

// Cancel asks a runner to stop a dispatched task (best-effort).
func (h *Hub) Cancel(runnerID, taskID string) {
	h.mu.Lock()
	c := h.conns[runnerID]
	h.mu.Unlock()
	if c == nil {
		return
	}
	select {
	case c.ch <- Dispatch{Kind: KindCancel, TaskID: taskID}:
	case <-c.done:
	case <-time.After(time.Second):
	}
}

func (h *Hub) forget(taskID string) {
	h.mu.Lock()
	delete(h.pending, taskID)
	h.mu.Unlock()
}

// DispatchHTTP pushes an outbound HTTP request to a connected runner and returns a channel that will
// receive its response (ADR-0025). Errors if the runner is offline or its stream is not draining.
func (h *Hub) DispatchHTTP(runnerID string, req HTTPRequest) (<-chan HTTPResult, error) {
	h.mu.Lock()
	c := h.conns[runnerID]
	if c == nil {
		h.mu.Unlock()
		return nil, ErrRunnerOffline
	}
	resCh := make(chan HTTPResult, 1)
	h.pendingHTTP[req.ID] = httpWaiter{runnerID: runnerID, ch: resCh}
	h.mu.Unlock()

	select {
	case c.ch <- Dispatch{Kind: KindHTTP, HTTP: &req}:
		return resCh, nil
	case <-c.done:
		h.ForgetHTTP(req.ID)
		return nil, ErrRunnerOffline
	case <-time.After(dispatchTimeout):
		h.ForgetHTTP(req.ID)
		return nil, ErrRunnerBusy
	}
}

// DeliverHTTP hands a runner's HTTP response to the waiting DispatchHTTP caller. It matches only when the
// delivering runner owns the request id, so a runner cannot answer another's request. Returns false if
// nothing was waiting or the owner didn't match.
func (h *Hub) DeliverHTTP(runnerID string, res HTTPResult) bool {
	h.mu.Lock()
	w, ok := h.pendingHTTP[res.ID]
	if !ok || w.runnerID != runnerID {
		h.mu.Unlock()
		return false
	}
	delete(h.pendingHTTP, res.ID)
	h.mu.Unlock()
	w.ch <- res // buffered (size 1), never blocks
	return true
}

// ForgetHTTP drops a pending HTTP request (the caller gave up).
func (h *Hub) ForgetHTTP(requestID string) {
	h.mu.Lock()
	delete(h.pendingHTTP, requestID)
	h.mu.Unlock()
}

// remoteRunner adapts a hub-connected runner to the runner.Runner interface, so the task engine dispatches
// to it exactly like the local Docker runner.
type remoteRunner struct {
	hub  *Hub
	id   string
	name string
}

// Runner returns a runner.Runner that executes RunSpecs on the given enrolled runner via the hub.
func (h *Hub) Runner(id, name string) runner.Runner {
	return &remoteRunner{hub: h, id: id, name: name}
}

func (r *remoteRunner) Name() string { return r.name }

func (r *remoteRunner) Run(ctx context.Context, spec runner.RunSpec) (runner.Result, error) {
	taskID := strings.TrimPrefix(spec.Name, "osb-")
	resCh, err := r.hub.Dispatch(r.id, taskID, spec)
	if err != nil {
		return runner.Result{}, err
	}
	select {
	case res := <-resCh:
		return res, nil
	case <-ctx.Done():
		r.hub.Cancel(r.id, taskID)
		r.hub.forget(taskID)
		return runner.Result{}, ctx.Err()
	}
}
