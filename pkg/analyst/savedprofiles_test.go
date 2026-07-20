package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestSaveProfileAndResolve(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	sp, err := svc.SaveProfile(ctx, "My Agent", "a custom one", "You are a bespoke reviewer.", []string{"search", "get_finding", "read_file"})
	if err != nil {
		t.Fatal(err)
	}
	p := svc.resolveProfile(ctx, sp.ID)
	if p.Name != "My Agent" || len(p.ToolSet()) != 3 {
		t.Fatalf("resolved profile = %+v (tools %d)", p, len(p.ToolSet()))
	}
	// The custom persona is wrapped with the non-overridable safety invariants.
	if !strings.Contains(p.SystemPrompt(), "bespoke reviewer") || !strings.Contains(p.SystemPrompt(), "Never invent") {
		t.Fatalf("system prompt = %q", p.SystemPrompt())
	}
}

func TestSaveProfileValidates(t *testing.T) {
	ctx := context.Background()
	svc := NewService(store.NewCombinedManager(migratedStore(t)), nil, nil, "", &llm.MockProvider{})

	if _, err := svc.SaveProfile(ctx, "n", "", "", []string{"search"}); err == nil {
		t.Error("empty persona should error")
	}
	if _, err := svc.SaveProfile(ctx, "n", "", "persona", []string{"not_a_tool"}); err == nil {
		t.Error("unknown tool should error")
	}
	if _, err := svc.SaveProfile(ctx, "n", "", "persona", nil); err == nil {
		t.Error("no tools should error (least privilege)")
	}
}

func TestPlaybookCanReferenceSavedProfile(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	sp, err := svc.SaveProfile(ctx, "Custom", "", "persona", []string{"search"})
	if err != nil {
		t.Fatal(err)
	}
	// A playbook step referencing the saved profile validates and saves.
	if _, err := svc.SavePlaybook(ctx, "PB", "", "goal", []PlaybookStep{{Key: "a", Profile: sp.ID, Instruction: "do it"}}, "manual"); err != nil {
		t.Fatalf("playbook with a saved profile should validate: %v", err)
	}
	// Deleting the profile → resolve falls back to the generalist (a run never fails to start).
	if err := db.DeleteSavedProfile(ctx, sp.ID); err != nil {
		t.Fatal(err)
	}
	if svc.resolveProfile(ctx, sp.ID).ID != "generalist" {
		t.Fatal("a deleted profile id should resolve to the generalist fallback")
	}
}
