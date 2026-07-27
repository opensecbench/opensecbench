package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// FindingsByMethodologyItem counts non-false-positive findings linked to an item through its evidence, with
// the worst severity — the "what we found" signal that sits alongside coverage (ADR-0056 P3).
func TestFindingsByMethodologyItem(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "P"})
	pid := proj.ID
	item := "web-app/xss"

	// Two observations under the same item, each backing a finding (one high, one medium) → count 2, worst high.
	o1, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "a", Severity: "high"})
	o2, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "b", Severity: "medium"})
	// A finding may only be backed by confirmed observations.
	_ = db.ReviewObservation(ctx, o1.ID, model.ReviewConfirmed)
	_ = db.ReviewObservation(ctx, o2.ID, model.ReviewConfirmed)
	f1, _ := db.CreateFinding(ctx, NewFinding{Title: "F1", Severity: "high", ObservationIDs: []string{o1.ID}})
	if _, err := db.CreateFinding(ctx, NewFinding{Title: "F2", Severity: "medium", ObservationIDs: []string{o2.ID}}); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkCoverageObservation(ctx, pid, item, o1.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.LinkCoverageObservation(ctx, pid, item, o2.ID); err != nil {
		t.Fatal(err)
	}

	m, err := db.FindingsByMethodologyItem(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	if m[item].Count != 2 || m[item].WorstSeverity != "high" {
		t.Fatalf("item findings = %+v, want count 2 worst high", m[item])
	}

	// A false-positive finding drops out of the count.
	if err := db.SetFindingStatus(ctx, f1.ID, model.FindingFalsePositive); err != nil {
		t.Fatal(err)
	}
	m2, _ := db.FindingsByMethodologyItem(ctx, pid)
	if m2[item].Count != 1 || m2[item].WorstSeverity != "medium" {
		t.Fatalf("after false-positive, item findings = %+v, want count 1 worst medium", m2[item])
	}
}
