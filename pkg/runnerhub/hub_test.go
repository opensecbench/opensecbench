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
