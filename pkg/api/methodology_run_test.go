package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// methodologyOnComplete flips a methodology item to tested and attaches the task's observations as evidence
// (ADR-0056) — the result→coverage seam, indifferent to whether a human or an agent produced the result.
func TestMethodologyOnCompleteFlipsCoverage(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := store.NewCombinedManager(db)
	engine := task.NewEngine(mgr, cas.Fixed(blobs), capability.BuiltIns(), fakeTaskRunner{})
	s := New(Deps{Store: mgr, Engine: engine, CAS: blobs})
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	pid := proj.ID
	obs, err := db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "secret", Severity: "high"})
	if err != nil {
		t.Fatal(err)
	}

	itemID := "web-app/secrets"
	oc := task.Outcome{
		Task:         model.Task{ID: "t1", CapabilityID: "trufflehog", ProjectID: &pid, MethodologyItemID: &itemID, Status: model.TaskSucceeded},
		Observations: []model.Observation{obs},
	}
	s.methodologyOnComplete(ctx, oc)

	// Coverage flipped to covered (tested).
	cov, err := db.ListCoverage(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	var status string
	for _, c := range cov {
		if c.ItemID == itemID {
			status = c.Status
		}
	}
	if status != model.CoverageCovered {
		t.Fatalf("item coverage = %q, want covered", status)
	}
	// The observation is attached to the item as evidence.
	counts, _ := db.CountCoverageEvidence(ctx, pid)
	if counts[itemID] != 1 {
		t.Fatalf("evidence count = %d, want 1", counts[itemID])
	}

	// A failed task lands the item at in_progress (tested-with-a-problem), not covered.
	itemID2 := "web-app/xss"
	s.methodologyOnComplete(ctx, task.Outcome{Task: model.Task{ID: "t2", CapabilityID: "semgrep", ProjectID: &pid, MethodologyItemID: &itemID2, Status: model.TaskFailed}})
	cov2, _ := db.ListCoverage(ctx, pid)
	var status2 string
	for _, c := range cov2 {
		if c.ItemID == itemID2 {
			status2 = c.Status
		}
	}
	if status2 != model.CoverageInProgress {
		t.Fatalf("failed-task item coverage = %q, want in_progress", status2)
	}
}
