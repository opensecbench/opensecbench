package analyst

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

func TestCreateObservation(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: proj.ID})

	out, err := exec(ctx, agent.ToolCall{Tool: "create_observation", Args: map[string]any{
		"title": "Hardcoded API key", "severity": "high", "detail": "in config", "location": "config.go:12",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var o model.Observation
	if err := json.Unmarshal([]byte(out), &o); err != nil {
		t.Fatal(err)
	}
	// Analyst-origin, unreviewed until a human confirms it (can't back a finding yet).
	if o.Origin != model.OriginThread || o.ReviewState != model.ReviewUnreviewed {
		t.Fatalf("provenance wrong: %+v", o)
	}
	if o.Severity != "high" || o.Title != "Hardcoded API key" || o.Location != "config.go:12" {
		t.Fatalf("observation = %+v", o)
	}

	// The invariant that keeps human and agent on one dataset: an agent-recorded observation must appear in
	// the project-scoped list the human's Observations tab and the agent's own list_observations both read.
	// (Regression guard: without project_id, the row is orphaned and invisible to both.)
	got, err := db.ListObservationsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != o.ID {
		t.Fatalf("agent observation not visible in the project-scoped list: %+v", got)
	}
}

// TestTriageObservation proves the batch-triage tool applies a dismissal with a rationale: the observation
// becomes rejected and the reason is recorded on it (auditable + visible to the human), reversibly.
func TestTriageObservation(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	o, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Origin: model.OriginTool, Title: "CVE in test dep", Severity: "high", Attributes: map[string]string{"reachable": "false"}})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: proj.ID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "triage_observation", Args: map[string]any{
		"id": o.ID, "disposition": "dismiss", "rationale": "unreachable — govulncheck proved uncalled",
	}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetObservation(ctx, o.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ReviewState != model.ReviewRejected {
		t.Fatalf("review state = %q, want rejected", got.ReviewState)
	}
	if got.Attributes["triage_rationale"] == "" || got.Attributes["triaged_by"] != "agent" {
		t.Fatalf("rationale/actor not recorded: %#v", got.Attributes)
	}
}

// TestCreateObservationDedups proves an agent-recorded observation flows through the engine's shared ingest:
// recording the same finding twice fingerprint-dedups to a single row (same as a scanner re-run), so agent
// and scanner output stay one dataset rather than piling up duplicates.
func TestCreateObservationDedups(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	mgr := store.NewCombinedManager(db)
	engine := task.NewEngine(mgr, cas.Fixed(blobs), capability.BuiltIns(), runner.LocalRunner{})
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	exec := Executor(ExecDeps{Mgr: mgr, Engine: engine, ProjectID: proj.ID})

	args := map[string]any{"title": "Hardcoded API key", "severity": "high", "detail": "in config", "location": "config.go:12"}
	if _, err := exec(ctx, agent.ToolCall{Tool: "create_observation", Args: args}); err != nil {
		t.Fatal(err)
	}
	if _, err := exec(ctx, agent.ToolCall{Tool: "create_observation", Args: args}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ListObservationsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("recording the same observation twice created %d rows, want 1 (dedup)", len(got))
	}
}
