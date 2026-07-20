package task

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// recordingRunner stands in for a remote runner: it records that it ran and echoes a canned result.
type recordingRunner struct {
	name string
	ran  atomic.Int32
}

func (r *recordingRunner) Name() string { return r.name }
func (r *recordingRunner) Run(context.Context, runner.RunSpec) (runner.Result, error) {
	r.ran.Add(1)
	return runner.Result{ExitCode: 0, Stdout: []byte("cmd/main.go\n")}, nil
}

func TestEngineDispatchesToRemoteRunner(t *testing.T) {
	db, blobs := openStore(t)
	// The local runner would be used if selection failed; make it fail so the test can't pass by accident.
	eng := NewEngine(store.NewCombinedManager(db), blobs, capability.BuiltIns(), fakeRunner{code: 2})
	defer eng.Close()
	remote := &recordingRunner{name: "edge-1"}
	eng.SetRunnerResolver(func(id string) (runner.Runner, error) {
		if id == "run-123" {
			return remote, nil
		}
		return nil, store.ErrNotFound
	})

	task, err := eng.Enqueue(context.Background(), RunRequest{
		CapabilityID: "source-inventory", TargetDir: "/repo", Actor: "human", RunnerID: "run-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	done := pollTask(t, eng, task.ID)
	if done.Status != model.TaskSucceeded {
		t.Fatalf("remote task status = %s (err=%q)", done.Status, done.Error)
	}
	if remote.ran.Load() != 1 {
		t.Fatalf("remote runner ran %d times, want 1", remote.ran.Load())
	}
}

// A pending task targeting a remote runner (e.g. left by a prior process) is reconstructed with its
// RunnerTarget and dispatched to the remote runner after a restart.
func TestEngineReconstructsRemoteTarget(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	seeded, err := db.CreateTask(ctx, store.NewTask{
		CapabilityID: "source-inventory", CapabilityVersion: "1.0.0",
		TargetDir: "/repo", Actor: "human", Runner: "edge-1", RunnerTarget: "run-123", Queued: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if seeded.RunnerTarget != "run-123" {
		t.Fatalf("seed runner_target = %q", seeded.RunnerTarget)
	}

	eng := NewEngine(store.NewCombinedManager(db), blobs, capability.BuiltIns(), fakeRunner{code: 2})
	defer eng.Close()
	remote := &recordingRunner{name: "edge-1"}
	eng.SetRunnerResolver(func(id string) (runner.Runner, error) { return remote, nil })

	if done := pollTask(t, eng, seeded.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("reconstructed remote task = %s (err=%q)", done.Status, done.Error)
	}
	if remote.ran.Load() != 1 {
		t.Fatalf("remote runner ran %d times, want 1", remote.ran.Load())
	}
}

func TestEngineRemoteTargetWithoutResolverFails(t *testing.T) {
	db, blobs := openStore(t)
	eng := NewEngine(store.NewCombinedManager(db), blobs, capability.BuiltIns(), fakeRunner{code: 0})
	defer eng.Close()
	// No resolver set → a remote-targeted task fails cleanly rather than running locally.
	task, err := eng.Enqueue(context.Background(), RunRequest{
		CapabilityID: "source-inventory", TargetDir: "/repo", RunnerID: "ghost",
	})
	if err != nil {
		t.Fatal(err)
	}
	if done := pollTask(t, eng, task.ID); done.Status != model.TaskFailed {
		t.Fatalf("remote task without resolver = %s, want failed", done.Status)
	}
}
