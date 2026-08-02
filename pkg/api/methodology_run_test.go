package api

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// methodologyOnComplete flips a methodology item to tested and attaches the task's observations as evidence
// (ADR-0056) — the result→coverage seam, indifferent to whether a human or an agent produced the result.
func TestMethodologyOnCompleteFlipsCoverage(t *testing.T) {
	db := storetest.New(t)
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

	// One capability run shared by two items: both must get the evidence and flip to covered (dedup path).
	itemID, itemShared := "web-app/secrets", "web-app/injection-sqli"
	oc := task.Outcome{
		Task:         model.Task{ID: "t1", CapabilityID: "opengrep", ProjectID: &pid, MethodologyItemIDs: []string{itemID, itemShared}, Status: model.TaskSucceeded},
		Observations: []model.Observation{obs},
	}
	s.methodologyOnComplete(ctx, oc)

	// Coverage flipped to covered (tested) for BOTH items.
	cov, err := db.ListCoverage(ctx, pid)
	if err != nil {
		t.Fatal(err)
	}
	covByItem := map[string]string{}
	for _, c := range cov {
		covByItem[c.ItemID] = c.Status
	}
	if covByItem[itemID] != model.CoverageCovered || covByItem[itemShared] != model.CoverageCovered {
		t.Fatalf("both shared items should be covered, got %q / %q", covByItem[itemID], covByItem[itemShared])
	}
	// The observation is attached to BOTH items as evidence.
	counts, _ := db.CountCoverageEvidence(ctx, pid)
	if counts[itemID] != 1 || counts[itemShared] != 1 {
		t.Fatalf("both shared items should have 1 evidence, got %d / %d", counts[itemID], counts[itemShared])
	}

	// A failed task lands the item at in_progress (tested-with-a-problem), not covered.
	itemID2 := "web-app/xss"
	s.methodologyOnComplete(ctx, task.Outcome{Task: model.Task{ID: "t2", CapabilityID: "opengrep", ProjectID: &pid, MethodologyItemIDs: []string{itemID2}, Status: model.TaskFailed}})
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
