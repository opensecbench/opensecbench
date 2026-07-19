package store

import (
	"context"
	"testing"
	"time"

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

func TestUsageSummary(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "engagement"})

	add := func(provider, mdl, agent string, in, out int) {
		if err := db.RecordUsage(ctx, model.UsageRecord{ProjectID: proj.ID, Provider: provider, Model: mdl, AgentType: agent, InputTokens: in, OutputTokens: out}); err != nil {
			t.Fatal(err)
		}
	}
	add("anthropic", "claude-opus-4-8", "lead", 1000, 400)
	add("anthropic", "claude-haiku-4-5", "triage", 200, 90)
	add("openai", "gpt-4o", "", 50, 10) // unattributed (no agent) — excluded from by-agent

	// A month boundary well in the past: every record counts toward "this month".
	past := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	sum, err := db.UsageSummary(ctx, past, 6)
	if err != nil {
		t.Fatal(err)
	}
	if sum.AllInput != 1250 || sum.AllOutput != 500 {
		t.Fatalf("all-time totals = %d in / %d out, want 1250 / 500", sum.AllInput, sum.AllOutput)
	}
	if sum.MonthInput != 1250 || sum.MonthOutput != 500 {
		t.Fatalf("month totals (past boundary) = %d in / %d out, want 1250 / 500", sum.MonthInput, sum.MonthOutput)
	}
	if len(sum.TopModels) != 3 || sum.TopModels[0].Model != "claude-opus-4-8" {
		t.Fatalf("top models = %+v, want opus first of 3", sum.TopModels)
	}
	// By-agent excludes the unattributed row; lead (1400) ranks above triage (290).
	if len(sum.TopAgents) != 2 || sum.TopAgents[0].AgentType != "lead" {
		t.Fatalf("top agents = %+v, want [lead, triage]", sum.TopAgents)
	}
	if sum.TopAgents[0].InputTokens != 1000 || sum.TopAgents[0].OutputTokens != 400 {
		t.Fatalf("lead agent tokens = %d/%d, want 1000/400", sum.TopAgents[0].InputTokens, sum.TopAgents[0].OutputTokens)
	}

	// A future boundary excludes every record from "this month" but not all-time.
	future := time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC)
	sum, err = db.UsageSummary(ctx, future, 6)
	if err != nil {
		t.Fatal(err)
	}
	if sum.MonthInput != 0 || sum.MonthOutput != 0 {
		t.Fatalf("month totals (future boundary) = %d / %d, want 0 / 0", sum.MonthInput, sum.MonthOutput)
	}
	if sum.AllInput != 1250 {
		t.Fatalf("all-time input should ignore the month boundary, got %d", sum.AllInput)
	}
}
