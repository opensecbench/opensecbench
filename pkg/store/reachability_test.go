package store

import (
	"context"
	"testing"
)

func TestReachabilityUpsert(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, err := db.CreateProject(ctx, NewProject{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}

	// Unknown until recorded; blank inputs are no-ops.
	if _, known := db.ReachabilityForCVE(ctx, proj.ID, "CVE-1"); known {
		t.Fatal("should be unknown before any verdict")
	}
	if err := db.SetReachability(ctx, "", "CVE-1", "", true, "govulncheck"); err != nil {
		t.Fatalf("blank project should be a no-op, got %v", err)
	}

	// Record a reachable verdict, then flip it — the unique (project, cve) row is overwritten.
	if err := db.SetReachability(ctx, proj.ID, "CVE-1", "pkg/a", true, "govulncheck"); err != nil {
		t.Fatal(err)
	}
	if reachable, known := db.ReachabilityForCVE(ctx, proj.ID, "CVE-1"); !known || !reachable {
		t.Fatalf("want known+reachable, got known=%v reachable=%v", known, reachable)
	}
	if err := db.SetReachability(ctx, proj.ID, "CVE-1", "pkg/a", false, "govulncheck"); err != nil {
		t.Fatal(err)
	}
	if reachable, known := db.ReachabilityForCVE(ctx, proj.ID, "CVE-1"); !known || reachable {
		t.Fatalf("verdict should have flipped to not-reachable, got known=%v reachable=%v", known, reachable)
	}

	// Verdicts are project-scoped.
	other, _ := db.CreateProject(ctx, NewProject{Name: "other"})
	if _, known := db.ReachabilityForCVE(ctx, other.ID, "CVE-1"); known {
		t.Fatal("reachability must be project-scoped")
	}
}
