package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestPlaybookStepsReferenceRealProfiles(t *testing.T) {
	valid := map[string]bool{}
	for _, p := range Profiles() {
		valid[p.ID] = true
	}
	for _, pb := range Playbooks() {
		if len(pb.Steps) == 0 {
			t.Errorf("playbook %q has no steps", pb.ID)
		}
		keys := map[string]bool{}
		for _, s := range pb.Steps {
			if !valid[s.Profile] {
				t.Errorf("playbook %q step %q references unknown profile %q", pb.ID, s.Key, s.Profile)
			}
			keys[s.Key] = true
		}
		// Dependencies must reference earlier step keys.
		for _, s := range pb.Steps {
			for _, d := range s.DependsOn {
				if !keys[d] {
					t.Errorf("playbook %q step %q depends on unknown step %q", pb.ID, s.Key, d)
				}
			}
		}
	}
}

func TestStartPlanValidates(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)

	// No provider configured.
	noProv := NewService(db, nil, nil, "", nil)
	if _, err := noProv.StartPlan(ctx, "proj", "onboarding"); err == nil {
		t.Fatal("StartPlan should require a provider")
	}
	// Unknown playbook.
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})
	if _, err := svc.StartPlan(ctx, "proj", "nope"); err == nil {
		t.Fatal("StartPlan should reject an unknown playbook")
	}
}

func TestRunPlanHappyPath(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	// Empty mock → every delegated sub-agent answers immediately, no tools.
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})

	pb, _ := PlaybookByID("onboarding")
	plan := model.Plan{ProjectID: projectID, PlaybookID: pb.ID, Goal: pb.Goal}
	for _, s := range pb.Steps {
		plan.Steps = append(plan.Steps, model.PlanStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn})
	}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created) // synchronous

	got, err := db.GetPlan(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.PlanDone {
		t.Fatalf("plan status = %q", got.Status)
	}
	for _, s := range got.Steps {
		if s.Status != model.StepDone {
			t.Fatalf("step %q status = %q", s.Key, s.Status)
		}
	}
}

func TestRunPlanBreaksOnCycle(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})

	// A depends on B, B depends on A — unresolvable.
	plan := model.Plan{ProjectID: projectID, PlaybookID: "custom", Steps: []model.PlanStep{
		{Key: "a", Profile: "report-writer", Instruction: "a", DependsOn: []string{"b"}},
		{Key: "b", Profile: "report-writer", Instruction: "b", DependsOn: []string{"a"}},
	}}
	created, err := db.CreatePlan(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	svc.runPlan(created)

	got, _ := db.GetPlan(ctx, created.ID)
	if got.Status != model.PlanFailed {
		t.Fatalf("a cyclic plan should fail, status = %q", got.Status)
	}
}

func TestPlansListedByProject(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	if _, err := db.CreatePlan(ctx, model.Plan{ProjectID: projectID, PlaybookID: "onboarding"}); err != nil {
		t.Fatal(err)
	}
	plans, err := db.ListPlansByProject(ctx, projectID)
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].PlaybookID != "onboarding" {
		t.Fatalf("plans = %+v", plans)
	}
}
