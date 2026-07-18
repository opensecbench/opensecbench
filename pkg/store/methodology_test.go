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
