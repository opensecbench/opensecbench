package task

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// A SAST finding recorded before its route was discovered is upgraded retroactively: once route-map adds
// the route whose handler is on the finding's dataflow path, ReEvaluate marks it route_reachable and
// opens an investigation — and re-running never duplicates it.
func TestReEvaluateUpgradesFindingWhenRouteArrivesLater(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")

	// A completed opengrep task (its manifest carries the SAST route-reachability routing).
	tk, err := db.CreateTask(ctx, store.NewTask{CapabilityID: "opengrep", CapabilityVersion: "1.0.0", TargetDir: "/repo", ApplicationID: &app.ID, Actor: "human", Runner: "local", Queued: true})
	if err != nil {
		t.Fatal(err)
	}
	// A low-severity taint finding with a dataflow trace through app/views.py — but no route exists yet,
	// so nothing escalates it.
	o, err := db.CreateObservation(ctx, model.Observation{
		TaskID: &tk.ID, ProjectID: &proj.ID, Origin: model.OriginTool, ReviewState: model.ReviewUnreviewed,
		Title: "SQL injection", Detail: "taint from request to query", Severity: "low",
		RuleID: "python.sql-injection", Location: "app/db.py:99",
		Attributes: map[string]string{"reachable": "true", "dataflow_path": "app/views.py:14,app/db.py:99"},
	})
	if err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{code: 0})
	defer eng.Close()

	// No route yet: re-eval changes nothing, opens nothing.
	eng.ReEvaluate(ctx, proj.ID)
	if invs, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(invs) != 0 {
		t.Fatalf("no route yet → no investigation, got %d", len(invs))
	}

	// route-map discovers the route whose handler is on the finding's dataflow path.
	if err := db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "POST", Path: "/login", HandlerFile: "app/views.py", HandlerLine: 10, Observed: true}); err != nil {
		t.Fatal(err)
	}

	eng.ReEvaluate(ctx, proj.ID)

	got, _ := db.GetObservation(ctx, o.ID)
	if got.Attributes["route_reachable"] != "true" {
		t.Fatalf("expected route_reachable=true after route arrived; attrs=%v", got.Attributes)
	}
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(invs) != 1 || invs[0].ObservationID != o.ID {
		t.Fatalf("expected 1 investigation for the observation, got %+v", invs)
	}

	// Idempotent: another re-eval must not open a second investigation.
	eng.ReEvaluate(ctx, proj.ID)
	if invs, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(invs) != 1 {
		t.Fatalf("re-eval should be idempotent; got %d investigations", len(invs))
	}
}
