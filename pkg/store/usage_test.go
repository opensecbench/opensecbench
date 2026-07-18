package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestUsageAggregation(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "engagement"})

	add := func(provider, mdl string, in, out int) {
		if err := db.RecordUsage(ctx, model.UsageRecord{ProjectID: proj.ID, Provider: provider, Model: mdl, InputTokens: in, OutputTokens: out}); err != nil {
			t.Fatal(err)
		}
	}
	add("anthropic", "claude-sonnet-5", 100, 50)
	add("anthropic", "claude-sonnet-5", 200, 80)
	add("claude-cli", "", 300, 120)
	add("anthropic", "claude-sonnet-5", 0, 0) // zero-token run is not recorded

	got, err := db.UsageByModel(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(got), got)
	}

	var anthropic *model.UsageByModel
	for i := range got {
		if got[i].Provider == "anthropic" {
			anthropic = &got[i]
		}
	}
	if anthropic == nil || anthropic.Runs != 2 || anthropic.InputTokens != 300 || anthropic.OutputTokens != 130 {
		t.Fatalf("anthropic aggregate = %+v, want 2 runs / 300 in / 130 out", anthropic)
	}
}
