package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// A plan left "running" by a restart is reconciled to failed with its steps resolved; a "waiting" plan
// (parked on a gate) is left alone.
func TestFailUnfinishedPlans(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "P"})

	// A ghost: running plan with a running step and a pending step.
	ghost, err := db.CreatePlan(ctx, model.Plan{ProjectID: proj.ID, PlaybookID: "onboarding", Status: model.PlanRunning, Steps: []model.PlanStep{
		{Key: "inventory", Profile: "code-analysis", Instruction: "x"},
		{Key: "surface", Profile: "code-analysis", Instruction: "y", DependsOn: []string{"inventory"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	steps, _ := db.listPlanSteps(ctx, ghost.ID)
	_ = db.setStepStatus(t, ctx, steps[0].ID, model.StepRunning) // in-flight when the process died

	// A legitimately parked plan.
	waiting, _ := db.CreatePlan(ctx, model.Plan{ProjectID: proj.ID, PlaybookID: "onboarding", Status: model.PlanWaiting})

	n, err := db.FailUnfinishedPlans(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("reconciled %d plans, want 1 (only the running one)", n)
	}

	g, _ := db.GetPlan(ctx, ghost.ID)
	if g.Status != model.PlanFailed {
		t.Fatalf("ghost plan status = %q, want failed", g.Status)
	}
	byKey := map[string]model.PlanStep{}
	for _, s := range g.Steps {
		byKey[s.Key] = s
	}
	if byKey["inventory"].Status != model.StepFailed {
		t.Fatalf("running step = %q, want failed", byKey["inventory"].Status)
	}
	if byKey["surface"].Status != model.StepSkipped {
		t.Fatalf("pending step = %q, want skipped", byKey["surface"].Status)
	}

	w, _ := db.GetPlan(ctx, waiting.ID)
	if w.Status != model.PlanWaiting {
		t.Fatalf("waiting plan should be untouched, got %q", w.Status)
	}
}

// setStepStatus is a tiny helper to force a step's status for the test.
func (db *DB) setStepStatus(t *testing.T, ctx context.Context, stepID, status string) error {
	t.Helper()
	_, err := db.ExecContext(ctx, `UPDATE plan_steps SET status = ? WHERE id = ?`, status, stepID)
	if err != nil {
		t.Fatal(err)
	}
	return nil
}
