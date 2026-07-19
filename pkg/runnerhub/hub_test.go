package runnerhub

import (
	"context"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

func TestDispatchDeliver(t *testing.T) {
	h := New()
	sub := h.Register("r1")
	defer sub.Close()

	if !h.Online("r1") {
		t.Fatal("r1 should be online after Register")
	}

	resCh, err := h.Dispatch("r1", "task-1", runner.RunSpec{Image: "x", Name: "osb-task-1"})
	if err != nil {
		t.Fatal(err)
	}
	// The dispatch reaches the runner's stream.
	select {
	case d := <-sub.Ch:
		if d.Kind != KindRun || d.TaskID != "task-1" {
			t.Fatalf("dispatch = %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no dispatch received")
	}
	// Delivering the result unblocks the waiter.
	if !h.Deliver("task-1", runner.Result{ExitCode: 0, Stdout: []byte("ok")}) {
		t.Fatal("Deliver should match the pending task")
	}
	select {
	case res := <-resCh:
		if string(res.Stdout) != "ok" {
			t.Fatalf("result = %q", res.Stdout)
		}
	case <-time.After(time.Second):
		t.Fatal("result not delivered to waiter")
	}
	// A second deliver for the same task matches nothing.
	if h.Deliver("task-1", runner.Result{}) {
		t.Fatal("second Deliver should not match")
	}
}

func TestDispatchHTTPDeliver(t *testing.T) {
	h := New()
	sub := h.Register("r1")
	defer sub.Close()

	resCh, err := h.DispatchHTTP("r1", HTTPRequest{ID: "req-1", Method: "GET", URL: "https://in.scope/x"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case d := <-sub.Ch:
		if d.Kind != KindHTTP || d.HTTP == nil || d.HTTP.ID != "req-1" {
			t.Fatalf("http dispatch = %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no http dispatch received")
	}
	if !h.DeliverHTTP("r1", HTTPResult{ID: "req-1", Status: 200, Body: "ok"}) {
		t.Fatal("DeliverHTTP should match the pending request")
	}
	select {
	case res := <-resCh:
		if res.Status != 200 || res.Body != "ok" {
			t.Fatalf("http result = %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("http result not delivered")
	}
}

func TestDeliverHTTPOwnershipAndUnknown(t *testing.T) {
	h := New()
	sub := h.Register("r1")
	defer sub.Close()
	if _, err := h.DispatchHTTP("r1", HTTPRequest{ID: "req-1", URL: "https://x/"}); err != nil {
		t.Fatal(err)
	}
	<-sub.Ch
	// A different runner cannot answer r1's request.
	if h.DeliverHTTP("r2", HTTPResult{ID: "req-1", Status: 200}) {
		t.Fatal("a foreign runner must not deliver another runner's request")
	}
	// An unknown request id matches nothing.
	if h.DeliverHTTP("r1", HTTPResult{ID: "nope"}) {
		t.Fatal("unknown request id should not match")
	}
	// The rightful owner still can.
	if !h.DeliverHTTP("r1", HTTPResult{ID: "req-1", Status: 204}) {
		t.Fatal("owning runner should deliver")
	}
}

func TestDispatchHTTPOfflineRunner(t *testing.T) {
	h := New()
	if _, err := h.DispatchHTTP("ghost", HTTPRequest{ID: "r"}); err != ErrRunnerOffline {
		t.Fatalf("DispatchHTTP to offline runner = %v, want ErrRunnerOffline", err)
	}
}

func TestDispatchOfflineRunner(t *testing.T) {
	h := New()
	if _, err := h.Dispatch("ghost", "t", runner.RunSpec{}); err != ErrRunnerOffline {
		t.Fatalf("dispatch to offline runner = %v, want ErrRunnerOffline", err)
	}
}

func TestCancelReachesRunner(t *testing.T) {
	h := New()
	sub := h.Register("r1")
	defer sub.Close()
	if _, err := h.Dispatch("r1", "t1", runner.RunSpec{Name: "osb-t1"}); err != nil {
		t.Fatal(err)
	}
	<-sub.Ch // drain the run dispatch
	h.Cancel("r1", "t1")
	select {
	case d := <-sub.Ch:
		if d.Kind != KindCancel || d.TaskID != "t1" {
			t.Fatalf("expected cancel for t1, got %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("cancel not delivered")
	}
}

func TestReconnectEvictsOldStream(t *testing.T) {
	h := New()
	first := h.Register("r1")
	second := h.Register("r1") // same runner reconnects
	defer second.Close()

	select {
	case <-first.Done: // evicted
	case <-time.After(time.Second):
		t.Fatal("the first stream should be evicted when the runner reconnects")
	}
	if !h.Online("r1") {
		t.Fatal("r1 should still be online via the second stream")
	}
}

// The remoteRunner adapter dispatches through the hub and returns the delivered result, and cancels on
// context cancellation.
func TestRemoteRunnerAdapter(t *testing.T) {
	h := New()
	sub := h.Register("r1")
	defer sub.Close()
	rr := h.Runner("r1", "edge-1")
	if rr.Name() != "edge-1" {
		t.Fatalf("Name = %q", rr.Name())
	}

	// Drive a run: a goroutine plays the runner, echoing a result.
	go func() {
		d := <-sub.Ch
		h.Deliver(d.TaskID, runner.Result{ExitCode: 0, Stdout: []byte("done")})
	}()
	res, err := rr.Run(context.Background(), runner.RunSpec{Name: "osb-abc"})
	if err != nil || string(res.Stdout) != "done" {
		t.Fatalf("Run = %q err=%v", res.Stdout, err)
	}

	// Cancellation returns promptly and emits a cancel to the runner.
	ctx, cancel := context.WithCancel(context.Background())
	go func() { <-sub.Ch; cancel() }() // consume the run dispatch, then cancel
	if _, err := rr.Run(ctx, runner.RunSpec{Name: "osb-xyz"}); err == nil {
		t.Fatal("Run should return the context error on cancellation")
	}
	select {
	case d := <-sub.Ch:
		if d.Kind != KindCancel {
			t.Fatalf("expected a cancel dispatch, got %+v", d)
		}
	case <-time.After(time.Second):
		t.Fatal("no cancel dispatch after context cancellation")
	}
}
