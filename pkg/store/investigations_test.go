package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestObservationAttributesRoundTrip(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	o, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Title: "secret", Origin: model.OriginTool,
		Attributes: map[string]string{"verified": "true", "detector": "AWS"},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetObservation(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attributes["verified"] != "true" || got.Attributes["detector"] != "AWS" {
		t.Fatalf("attributes not round-tripped: %+v", got.Attributes)
	}
}

func TestInvestigationLifecycle(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	o, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Title: "unverified secret", Origin: model.OriginTool})

	inv, err := db.CreateInvestigation(ctx, model.Investigation{ProjectID: proj.ID, ObservationID: o.ID, Title: "Validate secret"})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Status != model.InvestigationOpen {
		t.Fatalf("new investigation status = %s, want open", inv.Status)
	}
	// One per observation — a repeat returns the same record.
	again, _ := db.CreateInvestigation(ctx, model.Investigation{ProjectID: proj.ID, ObservationID: o.ID, Title: "dup"})
	if again.ID != inv.ID {
		t.Fatal("second investigation for the same observation should return the existing one")
	}

	th, err := db.CreateThread(ctx, NewThread{ProjectID: &proj.ID, Title: "Investigate", AgentType: "vuln-validator"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetInvestigationThread(ctx, inv.ID, th.ID); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetInvestigation(ctx, inv.ID)
	if got.Status != model.InvestigationInvestigating || got.ThreadID == nil || *got.ThreadID != th.ID {
		t.Fatalf("after run = %+v, want investigating + thread", got)
	}
	if err := db.SetInvestigationStatus(ctx, inv.ID, model.InvestigationResolved); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(list) != 1 || list[0].Status != model.InvestigationResolved {
		t.Fatalf("list = %+v, want 1 resolved", list)
	}
}

func TestDispositionRuleCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	r, err := db.SetDispositionRule(ctx, model.DispositionRule{
		ProjectID: proj.ID, CapabilityID: "trufflehog",
		When: map[string]string{"verified": "true"}, Action: "finding", Priority: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := db.ListDispositionRules(ctx, proj.ID)
	if len(list) != 1 || list[0].When["verified"] != "true" || list[0].Action != "finding" {
		t.Fatalf("list = %+v", list)
	}
	if err := db.DeleteDispositionRule(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if list, _ := db.ListDispositionRules(ctx, proj.ID); len(list) != 0 {
		t.Fatalf("after delete = %d, want 0", len(list))
	}
}
