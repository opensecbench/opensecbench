package task

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestEnginePublishesDomainEvents confirms the engine emits live events (ADR-0063): every finished task
// announces task.completed, and a disposition that promotes an observation announces finding.created —
// so clients stream scan progress and new findings instead of polling.
//
// task.completed fires in a defer as execute unwinds, just after the task's status flips to succeeded, so
// the assertion waits for the events rather than snapshotting the instant pollTask returns.
func TestEnginePublishesDomainEvents(t *testing.T) {
	eng, db := dispoEngine(t)
	ctx := context.Background()

	var mu sync.Mutex
	byType := map[string]int{}
	var findingPayloadOK, taskPayloadOK bool
	eng.SetPublisher(func(projectID, eventType string, payload any) {
		mu.Lock()
		defer mu.Unlock()
		byType[eventType]++
		switch eventType {
		case "finding.created":
			if f, ok := payload.(model.Finding); ok && f.Title != "" {
				findingPayloadOK = true
			}
		case "task.completed":
			if m, ok := payload.(map[string]any); ok {
				if _, has := m["task"]; has {
					taskPayloadOK = true
				}
			}
		}
	})

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")

	tk, err := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-secrets", TargetDir: "/repo", ApplicationID: &app.ID})
	if err != nil {
		t.Fatal(err)
	}
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	// Wait for both events (the verified secret promotes to a finding; the unverified one opens an
	// investigation, so exactly one finding.created).
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		done := byType["task.completed"] == 1 && byType["finding.created"] == 1
		mu.Unlock()
		if done || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	mu.Lock()
	defer mu.Unlock()
	if byType["task.completed"] != 1 {
		t.Errorf("task.completed events = %d, want 1", byType["task.completed"])
	}
	if byType["finding.created"] != 1 {
		t.Errorf("finding.created events = %d, want 1", byType["finding.created"])
	}
	if !taskPayloadOK {
		t.Error("task.completed payload missing task")
	}
	if !findingPayloadOK {
		t.Error("finding.created payload was not a model.Finding")
	}
}
