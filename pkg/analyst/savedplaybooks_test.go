package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestSavePlaybookAndResolve(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})

	steps := []PlaybookStep{
		{Key: "a", Profile: "code-analysis", Instruction: "look at the code"},
		{Key: "b", Profile: "report-writer", Instruction: "write it up", DependsOn: []string{"a"}},
	}
	sp, err := svc.SavePlaybook(ctx, "My playbook", "desc", "a goal", steps, "manual")
	if err != nil {
		t.Fatal(err)
	}

	// resolvePlaybook finds the saved one (built-ins take precedence, but this id is novel).
	pb, err := svc.resolvePlaybook(ctx, sp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pb.Name != "My playbook" || len(pb.Steps) != 2 || pb.Steps[1].DependsOn[0] != "a" {
		t.Fatalf("resolved playbook = %+v", pb)
	}

	// It shows up in the store list and can be deleted.
	list, _ := db.ListSavedPlaybooks(ctx)
	if len(list) != 1 {
		t.Fatalf("saved list = %d", len(list))
	}
	if err := db.DeleteSavedPlaybook(ctx, sp.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.resolvePlaybook(ctx, sp.ID); err == nil {
		t.Fatal("deleted playbook should not resolve")
	}
}

func TestSavePlaybookValidates(t *testing.T) {
	ctx := context.Background()
	svc := NewService(migratedStore(t), nil, nil, "", &llm.MockProvider{})

	cases := map[string][]PlaybookStep{
		"unknown profile":     {{Key: "a", Profile: "nope", Instruction: "x"}},
		"bad dependency":      {{Key: "a", Profile: "report-writer", Instruction: "x", DependsOn: []string{"z"}}},
		"missing instruction": {{Key: "a", Profile: "report-writer"}},
	}
	for name, steps := range cases {
		if _, err := svc.SavePlaybook(ctx, "n", "", "", steps, "manual"); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	// No name.
	if _, err := svc.SavePlaybook(ctx, "", "", "", []PlaybookStep{{Key: "a", Profile: "report-writer", Instruction: "x"}}, "manual"); err == nil {
		t.Error("empty name should error")
	}
}

func TestSavePlaybookFromPlan(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})

	created, err := db.CreatePlan(ctx, model.Plan{ProjectID: projectID, PlaybookID: "onboarding", Goal: "baseline", Steps: []model.PlanStep{
		{Key: "a", Profile: "code-analysis", Instruction: "look"},
		{Key: "b", Profile: "report-writer", Instruction: "write", DependsOn: []string{"a"}},
	}})
	if err != nil {
		t.Fatal(err)
	}

	sp, err := svc.SavePlaybookFromPlan(ctx, created.ID, "Recorded run", "from a plan")
	if err != nil {
		t.Fatal(err)
	}
	if sp.Source != "plan:"+created.ID {
		t.Fatalf("source = %q", sp.Source)
	}
	pb, _ := svc.resolvePlaybook(ctx, sp.ID)
	if pb.Goal != "baseline" || len(pb.Steps) != 2 || pb.Steps[1].Profile != "report-writer" {
		t.Fatalf("recorded playbook = %+v", pb)
	}
}
