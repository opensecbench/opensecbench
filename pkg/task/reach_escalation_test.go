package task

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// A reachability fact (as a human or LLM would add after tracing dynamic code) resolves to a confirmed
// reachable verdict, which re-evaluation folds onto the finding and disposition escalates — even though
// the static tools left it as a low-severity, un-routed observation.
func TestManualReachabilityEscalatesFinding(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	tk, _ := db.CreateTask(ctx, store.NewTask{CapabilityID: "opengrep", CapabilityVersion: "1.0.0", TargetDir: "/repo", ApplicationID: &app.ID, Actor: "human", Runner: "local", Queued: true})

	// Low-severity SAST finding, no route, nothing to escalate it statically.
	o, _ := db.CreateObservation(ctx, model.Observation{
		TaskID: &tk.ID, ProjectID: &proj.ID, Origin: model.OriginTool, ReviewState: model.ReviewUnreviewed,
		Title: "command injection", Detail: "possible", Severity: "low",
		RuleID: "python.command-injection", Location: "app/util.py:20",
	})

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{code: 0})
	defer eng.Close()

	// Nothing yet: re-eval opens no investigation.
	eng.ReEvaluate(ctx, proj.ID)
	if invs, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(invs) != 0 {
		t.Fatalf("no reachability fact yet → no escalation, got %d", len(invs))
	}

	// A human/LLM traces reachability the static tools couldn't and records it.
	if err := db.AddReachabilityFact(ctx, model.ReachabilityFact{
		ProjectID: proj.ID, SubjectType: model.ReachSubjectObservation, SubjectKey: o.ID,
		Reachable: model.ReachReachable, Confidence: model.ReachConfHigh, Source: "llm",
		Method: "LLM analysis", Rationale: "reached via a getattr dispatch from the /run handler",
	}); err != nil {
		t.Fatal(err)
	}

	eng.ReEvaluate(ctx, proj.ID)

	got, _ := db.GetObservation(ctx, o.ID)
	if got.Attributes["reachable_confirmed"] != "true" {
		t.Fatalf("expected reachable_confirmed=true after the fact; attrs=%v", got.Attributes)
	}
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(invs) != 1 || invs[0].ObservationID != o.ID {
		t.Fatalf("confirmed reachability should escalate to an investigation, got %+v", invs)
	}
}
