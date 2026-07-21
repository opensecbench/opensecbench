package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// The record_reachability tool writes an LLM-sourced reachability fact that aggregates with tool verdicts.
func TestRecordReachabilityTool(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: proj.ID})

	out, err := exec(ctx, agent.ToolCall{Tool: "record_reachability", Args: map[string]any{
		"subject_type": "observation", "subject": "obs-1",
		"reachable": "reachable", "confidence": "high",
		"rationale": "the sink is called from the @app.route('/pay') handler via a dynamic dispatch table",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "recorded") {
		t.Fatalf("unexpected output: %s", out)
	}

	v, c, facts := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectObservation, "obs-1")
	if v != model.ReachReachable || c != model.ReachConfHigh || len(facts) != 1 {
		t.Fatalf("resolve = (%s,%s,%d facts), want reachable/high/1", v, c, len(facts))
	}
	if facts[0].Source != "llm" {
		t.Fatalf("fact source = %q, want llm", facts[0].Source)
	}
}
