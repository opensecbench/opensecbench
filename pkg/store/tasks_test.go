package store

import (
	"context"
	"sync"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestTaskAndArtifactProvenance(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, NewTask{
		CapabilityID:      "semgrep",
		CapabilityVersion: "1.0.0",
		Actor:             "human:james",
		Runner:            "local-docker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskRunning || task.StartedAt == nil {
		t.Fatalf("new task not running/started: %+v", task)
	}

	art, err := db.CreateArtifact(ctx, model.Artifact{
		TaskID: &task.ID,
		SHA256: "abc123",
		Size:   42,
		Kind:   model.ArtifactOutput,
		Name:   "semgrep.sarif",
	})
	if err != nil {
		t.Fatal(err)
	}

	code := 0
	if err := db.FinishTask(ctx, task.ID, model.TaskSucceeded, &code, ""); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskSucceeded || got.ExitCode == nil || *got.ExitCode != 0 || got.FinishedAt == nil {
		t.Fatalf("finished task not recorded correctly: %+v", got)
	}

	arts, err := db.ListArtifactsByTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].ID != art.ID || arts[0].Name != "semgrep.sarif" {
		t.Fatalf("artifact provenance not linked: %+v", arts)
	}
}

func TestFinishUnknownTask(t *testing.T) {
	db := migratedDB(t)
	if err := db.FinishTask(context.Background(), "nope", model.TaskFailed, nil, "x"); err != ErrNotFound {
		t.Fatalf("FinishTask(unknown) = %v, want ErrNotFound", err)
	}
}

func queued(cap string) NewTask {
	return NewTask{CapabilityID: cap, CapabilityVersion: "1.0.0", Actor: "human", Runner: "fake", Queued: true}
}

func TestCreateTaskPersistsReconstructionData(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	nt := queued("http-probe")
	nt.TargetDir = "/repo/x"
	nt.SecretRefs = map[string]string{"AUTH": "api_token"}
	created, err := db.CreateTask(ctx, nt)
	if err != nil {
		t.Fatal(err)
	}
	if created.Status != model.TaskPending || created.StartedAt != nil {
		t.Fatalf("queued task should be pending with no started_at: %+v", created)
	}
	got, err := db.GetTask(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetDir != "/repo/x" || got.SecretRefs["AUTH"] != "api_token" {
		t.Fatalf("reconstruction data not round-tripped: target=%q refs=%+v", got.TargetDir, got.SecretRefs)
	}
}

func TestClaimNextPendingTaskAtomic(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	a, _ := db.CreateTask(ctx, queued("source-inventory"))
	b, _ := db.CreateTask(ctx, queued("source-inventory"))

	// Two claims return the two distinct tasks (oldest first), each flipped to running with attempts=1.
	first, ok, err := db.ClaimNextPendingTask(ctx)
	if err != nil || !ok {
		t.Fatalf("first claim ok=%v err=%v", ok, err)
	}
	if first.ID != a.ID || first.Status != model.TaskRunning || first.Attempts != 1 || first.StartedAt == nil {
		t.Fatalf("first claim = %+v, want task a running attempts=1", first)
	}
	second, ok, _ := db.ClaimNextPendingTask(ctx)
	if !ok || second.ID != b.ID {
		t.Fatalf("second claim = %+v ok=%v, want task b", second, ok)
	}
	// Queue now empty.
	if _, ok, _ := db.ClaimNextPendingTask(ctx); ok {
		t.Fatal("third claim should find nothing")
	}
}

func TestRequeueInterruptedTasks(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	running, _ := db.CreateTask(ctx, NewTask{CapabilityID: "source-inventory", CapabilityVersion: "1.0.0", Actor: "h", Runner: "fake"}) // running
	pending, _ := db.CreateTask(ctx, queued("source-inventory"))
	done, _ := db.CreateTask(ctx, NewTask{CapabilityID: "source-inventory", CapabilityVersion: "1.0.0", Actor: "h", Runner: "fake"})
	_ = db.FinishTask(ctx, done.ID, model.TaskSucceeded, nil, "")

	n, err := db.RequeueInterruptedTasks(ctx)
	if err != nil || n != 1 {
		t.Fatalf("requeued %d err=%v, want 1 (only the running one)", n, err)
	}
	if got, _ := db.GetTask(ctx, running.ID); got.Status != model.TaskPending || got.StartedAt != nil {
		t.Fatalf("interrupted task = %+v, want pending with started_at cleared", got)
	}
	if got, _ := db.GetTask(ctx, pending.ID); got.Status != model.TaskPending {
		t.Fatal("already-pending task should stay pending")
	}
	if got, _ := db.GetTask(ctx, done.ID); got.Status != model.TaskSucceeded {
		t.Fatal("finished task must be untouched")
	}
}

func TestClaimNextPendingTaskNoDoubleClaim(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	const n = 20
	for i := 0; i < n; i++ {
		if _, err := db.CreateTask(ctx, queued("source-inventory")); err != nil {
			t.Fatal(err)
		}
	}

	// Many workers claim concurrently; every task must be claimed by exactly one.
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				task, ok, err := db.ClaimNextPendingTask(ctx)
				if err != nil || !ok {
					return
				}
				mu.Lock()
				seen[task.ID]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()

	if len(seen) != n {
		t.Fatalf("claimed %d distinct tasks, want %d", len(seen), n)
	}
	for id, c := range seen {
		if c != 1 {
			t.Fatalf("task %s claimed %d times (double-claim)", id, c)
		}
	}
}

func TestCancelPendingTask(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	p, _ := db.CreateTask(ctx, queued("source-inventory"))

	ok, err := db.CancelPendingTask(ctx, p.ID)
	if err != nil || !ok {
		t.Fatalf("cancel pending ok=%v err=%v, want true", ok, err)
	}
	got, _ := db.GetTask(ctx, p.ID)
	if got.Status != model.TaskFailed || got.Error != "cancelled by user" {
		t.Fatalf("cancelled task = %+v", got)
	}
	// Cancelling a non-pending task is a no-op match.
	if ok, _ := db.CancelPendingTask(ctx, p.ID); ok {
		t.Fatal("cancelling an already-failed task should not match")
	}
}
