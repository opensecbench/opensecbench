package task

import (
	"context"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// RunMethodologyChecks stamps each spawned task with its item + run id, gates by applicability like
// ScanProject, and the on-complete hook fires with that attribution intact (ADR-0056).
func TestRunMethodologyChecksAttributesAndFiresHook(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	reg := capability.NewRegistry()
	reg.Register(pyOnlyCap{}) // source_repo + python
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), reg, fakeRunner{out: []byte("x"), code: 0})
	defer eng.Close()

	done := make(chan Outcome, 4)
	eng.SetOnComplete(func(_ context.Context, oc Outcome) { done <- oc })

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	seedRepoAsset(t, db, proj.ID, "requirements.txt", "flask\n") // python repo

	res, err := eng.RunMethodologyChecks(ctx, proj.ID, "run-1", []MethodologyCheck{{ItemID: "web-app/xss", CapabilityID: "py-checker"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Enqueued) != 1 {
		t.Fatalf("enqueued %d, want 1 (skips=%v)", len(res.Enqueued), res.Skipped)
	}
	tk := res.Enqueued[0]
	if tk.MethodologyItemID == nil || *tk.MethodologyItemID != "web-app/xss" || tk.MethodologyRunID == nil || *tk.MethodologyRunID != "run-1" {
		t.Fatalf("task not attributed to item/run: %+v", tk)
	}

	// The worker completes it and the hook fires with the same attribution.
	select {
	case oc := <-done:
		if oc.Task.MethodologyItemID == nil || *oc.Task.MethodologyItemID != "web-app/xss" {
			t.Fatalf("on-complete task lost its item id: %+v", oc.Task)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("on-complete hook did not fire")
	}

	// A check whose capability applies to no asset (or is unknown) is a recorded skip, not a run.
	res2, _ := eng.RunMethodologyChecks(ctx, proj.ID, "run-2", []MethodologyCheck{{ItemID: "x", CapabilityID: "nonexistent"}})
	if len(res2.Enqueued) != 0 || len(res2.Skipped) == 0 {
		t.Fatalf("unknown capability should be skipped, got %+v", res2)
	}
}

// A capability that opts out of auto-scan (empty AppliesTo, e.g. semgrep) must still run when an operator
// names it explicitly as a methodology check, against source repos (ADR-0056 P3.1).
func TestRunMethodologyChecksRunsOptOutCapability(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("x"), code: 0})
	defer eng.Close()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	seedRepoAsset(t, db, proj.ID, "app.py", "print(1)\n") // a source_repo asset

	res, err := eng.RunMethodologyChecks(ctx, proj.ID, "r", []MethodologyCheck{{ItemID: "x/y", CapabilityID: "semgrep"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Enqueued) != 1 || res.Enqueued[0].CapabilityID != "semgrep" {
		t.Fatalf("opt-out capability not run as an explicit check: enqueued=%d skipped=%v", len(res.Enqueued), res.Skipped)
	}
}
