package analyst

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
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
