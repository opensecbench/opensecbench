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
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db)})

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
}
