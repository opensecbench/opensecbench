package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestMethodologyAdoptionAndCoverage(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "engagement"})

	// Adoption is idempotent.
	for i := 0; i < 2; i++ {
		if err := db.AdoptMethodology(ctx, proj.ID, "web-app"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.AdoptMethodology(ctx, proj.ID, "rest-api"); err != nil {
		t.Fatal(err)
	}
	adopted, err := db.ListAdoptedMethodologies(ctx, proj.ID)
	if err != nil || len(adopted) != 2 {
		t.Fatalf("adopted = %v (%v), want 2", adopted, err)
	}

	// Coverage upsert: set then update the same item.
	if err := db.SetCoverage(ctx, proj.ID, "web-app/xss", model.CoverageInProgress, "started"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetCoverage(ctx, proj.ID, "web-app/xss", model.CoverageCovered, "output encoded"); err != nil {
		t.Fatal(err)
	}
	cov, err := db.ListCoverage(ctx, proj.ID)
	if err != nil || len(cov) != 1 {
		t.Fatalf("coverage = %v (%v), want 1 row", cov, err)
	}
	if cov[0].Status != model.CoverageCovered || cov[0].Note != "output encoded" {
		t.Fatalf("upsert wrong: %+v", cov[0])
	}

	// Invalid status rejected.
	if err := db.SetCoverage(ctx, proj.ID, "web-app/csrf", "bogus", ""); err == nil {
		t.Fatal("expected invalid status error")
	}

	// Unadopt drops from the list.
	if err := db.UnadoptMethodology(ctx, proj.ID, "rest-api"); err != nil {
		t.Fatal(err)
	}
	adopted, _ = db.ListAdoptedMethodologies(ctx, proj.ID)
	if len(adopted) != 1 || adopted[0] != "web-app" {
		t.Fatalf("after unadopt = %v, want [web-app]", adopted)
	}
}

func TestCoverageObservationLink(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "engagement"})

	mk := func(title string) model.Observation {
		o, err := db.CreateObservation(ctx, model.Observation{
			Origin: model.OriginHuman, ReviewState: model.ReviewUnreviewed, Title: title, Severity: "info",
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}
	obsA, obsB := mk("req A"), mk("req B")

	item := "rest-api/bola"
	// Linking is idempotent; two distinct observations on one item count as two.
	for i := 0; i < 2; i++ {
		if err := db.LinkCoverageObservation(ctx, proj.ID, item, obsA.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.LinkCoverageObservation(ctx, proj.ID, item, obsB.ID); err != nil {
		t.Fatal(err)
	}

	counts, err := db.CountCoverageEvidence(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if counts[item] != 2 {
		t.Fatalf("evidence count for %s = %d, want 2", item, counts[item])
	}

	// Missing arguments are rejected.
	if err := db.LinkCoverageObservation(ctx, proj.ID, "", obsA.ID); err == nil {
		t.Fatal("expected error for empty item id")
	}
}
