package store

import (
	"context"
	"errors"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/action"
)

// TestActionCRUD exercises the global action-definition store round-trip (ADR-0059): create → get → list →
// update → delete, plus validation, against the two-tier schema the app actually runs.
func TestActionCRUD(t *testing.T) {
	ctx := context.Background()
	g := openManager(t).Global()

	// A well-formed agent action round-trips, JSON fields intact.
	created, err := g.CreateAction(ctx, action.Action{
		Name: "Hunt logs", Kind: action.KindAgent, SubjectKinds: []string{"finding", "observation"},
		ProfileID: "generalist", Instruction: "check {{subject.title}}",
		AppliesWhen: action.Predicate{MinSeverity: "high"}, Technique: "intrusive",
		Output: action.OutputSpec{RecordObservations: true},
	})
	if err != nil {
		t.Fatalf("CreateAction: %v", err)
	}
	if created.ID == "" || created.CreatedAt.IsZero() {
		t.Fatal("create should assign an id and timestamps")
	}

	got, err := g.GetAction(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetAction: %v", err)
	}
	if got.Instruction != "check {{subject.title}}" || got.AppliesWhen.MinSeverity != "high" ||
		len(got.SubjectKinds) != 2 || !got.Output.RecordObservations {
		t.Fatalf("round-trip lost fields: %+v", got)
	}

	// Update mutates and bumps updated_at.
	got.Name = "Hunt logs v2"
	updated, err := g.UpdateAction(ctx, got)
	if err != nil {
		t.Fatalf("UpdateAction: %v", err)
	}
	if updated.Name != "Hunt logs v2" {
		t.Fatalf("update didn't persist: %q", updated.Name)
	}

	list, err := g.ListActions(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListActions = %d, %v; want 1", len(list), err)
	}

	if err := g.DeleteAction(ctx, created.ID); err != nil {
		t.Fatalf("DeleteAction: %v", err)
	}
	if _, err := g.GetAction(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v; want ErrNotFound", err)
	}
}

func TestActionValidation(t *testing.T) {
	ctx := context.Background()
	g := openManager(t).Global()
	bad := []action.Action{
		{Name: "", Kind: action.KindAgent, SubjectKinds: []string{"finding"}, ProfileID: "p", Instruction: "i"}, // no name
		{Name: "x", Kind: action.KindAgent, SubjectKinds: nil, ProfileID: "p", Instruction: "i"},                // no subject kind
		{Name: "x", Kind: action.KindAgent, SubjectKinds: []string{"finding"}},                                  // agent w/o profile+instruction
		{Name: "x", Kind: action.KindScript, SubjectKinds: []string{"finding"}},                                 // script w/o image+cmd
		{Name: "x", Kind: "bogus", SubjectKinds: []string{"finding"}},                                           // unknown kind
	}
	for i, a := range bad {
		if _, err := g.CreateAction(ctx, a); err == nil {
			t.Errorf("case %d: expected validation error, got nil", i)
		}
	}
}

// TestActionRunLifecycle exercises the per-project run history (ADR-0059): create running → finish → list
// by subject.
func TestActionRunLifecycle(t *testing.T) {
	ctx := context.Background()
	pdb, err := openManager(t).Project("proj-a")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}

	run, err := pdb.CreateActionRun(ctx, action.Run{
		ActionID: "a1", ActionName: "Hunt logs", Kind: action.KindAgent,
		SubjectKind: "observation", SubjectID: "obs-1",
	})
	if err != nil {
		t.Fatalf("CreateActionRun: %v", err)
	}
	if run.Status != action.RunRunning {
		t.Fatalf("new run status = %q, want running", run.Status)
	}

	if err := pdb.FinishActionRun(ctx, run.ID, action.RunDone, "found 2 hits", "full output", "art-1", ""); err != nil {
		t.Fatalf("FinishActionRun: %v", err)
	}
	got, err := pdb.GetActionRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetActionRun: %v", err)
	}
	if got.Status != action.RunDone || got.Summary != "found 2 hits" || got.ArtifactID != "art-1" || got.FinishedAt == nil {
		t.Fatalf("finish didn't persist: %+v", got)
	}

	// A different subject's runs don't leak in.
	if _, err := pdb.CreateActionRun(ctx, action.Run{ActionID: "a1", SubjectKind: "finding", SubjectID: "f-9"}); err != nil {
		t.Fatalf("second CreateActionRun: %v", err)
	}
	runs, err := pdb.ListActionRunsBySubject(ctx, "observation", "obs-1")
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListActionRunsBySubject = %d, %v; want 1", len(runs), err)
	}
}
