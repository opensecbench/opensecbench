// Package runnerhub brokers work between the control plane and connected remote runners (ADR-0024). A
// runner dials home and holds open a downstream stream; the hub pushes task dispatches (and cancels)
// down it and matches the runner's posted results back to the waiting caller. It is the in-process
// rendezvous between the durable task queue (which produces RunSpecs) and the runner protocol.
package runnerhub

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Dispatch messages flow down a runner's stream.
const (
	KindRun    = "run"
	KindCancel = "cancel"
)

// Dispatch is one message on a runner's downstream stream: run a task, or cancel one already dispatched.
type Dispatch struct {
	Kind   string         `json:"kind"`
	TaskID string         `json:"task_id"`
	Spec   runner.RunSpec `json:"spec,omitempty"`
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

// Hub is the control-plane-side broker. Safe for concurrent use.
type Hub struct {
	mu      sync.Mutex
	conns   map[string]*conn              // runnerID -> live stream
	pending map[string]chan runner.Result // taskID -> result waiter
}

// New builds an empty hub.
func New() *Hub {
	return &Hub{conns: map[string]*conn{}, pending: map[string]chan runner.Result{}}
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
