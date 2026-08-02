package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestConnectionModelsRoundTrip(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	conn, err := db.CreateProvider(ctx, model.Provider{Name: "cloud", Type: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if !conn.ModelsRefreshedAt.IsZero() {
		t.Fatalf("new connection should have zero models_refreshed_at, got %v", conn.ModelsRefreshedAt)
	}

	want := []model.ConnectionModel{
		{ModelID: "claude-sonnet-5", DisplayName: "Claude Sonnet 5", Family: "sonnet", ContextWindow: 1000000, InputPerMTok: 3, OutputPerMTok: 15, Tags: []string{"default", "fast"}, Source: "live"},
		{ModelID: "claude-opus-4-8", Family: "opus", Source: "live"},
	}
	if err := db.ReplaceConnectionModels(ctx, conn.ID, want); err != nil {
		t.Fatal(err)
	}

	got, err := db.ListConnectionModels(ctx, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	// Ordered by family then id: opus before sonnet.
	if got[0].ModelID != "claude-opus-4-8" || got[1].ModelID != "claude-sonnet-5" {
		t.Fatalf("unexpected order: %s, %s", got[0].ModelID, got[1].ModelID)
	}
	s5 := got[1]
	if s5.ContextWindow != 1000000 || len(s5.Tags) != 2 || s5.Tags[0] != "default" || s5.LastSeen.IsZero() {
		t.Fatalf("sonnet round-trip lost data: %+v", s5)
	}

	// Refresh stamps the connection and is a full replace, not an append.
	refreshed, err := db.GetProvider(ctx, conn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.ModelsRefreshedAt.IsZero() {
		t.Fatal("ReplaceConnectionModels did not stamp models_refreshed_at")
	}
	if err := db.ReplaceConnectionModels(ctx, conn.ID, want[:1]); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ListConnectionModels(ctx, conn.ID)
	if len(got) != 1 {
		t.Fatalf("replace should shrink to 1, got %d", len(got))
	}
}

// A per-model clearance override is operator intent and must survive a re-discovery that doesn't set it.
func TestConnectionModelClearancePreservedAcrossRefresh(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// A new connection defaults to the least-privilege clearance.
	conn, err := db.CreateProvider(ctx, model.Provider{Name: "cloud", Type: "anthropic"})
	if err != nil {
		t.Fatal(err)
	}
	if conn.DataClearance != model.DefaultClearance {
		t.Fatalf("new connection clearance = %q, want %q", conn.DataClearance, model.DefaultClearance)
	}

	discovered := []model.ConnectionModel{{ModelID: "claude-sonnet-5", Source: "live"}, {ModelID: "claude-fable-5", Source: "live"}}
	if err := db.ReplaceConnectionModels(ctx, conn.ID, discovered); err != nil {
		t.Fatal(err)
	}
	// Pin fable lower than the connection (e.g. retention not covered by the DPA).
	if err := db.SetConnectionModelClearance(ctx, conn.ID, "claude-fable-5", model.SensitivityInternal, "30-day retention"); err != nil {
		t.Fatal(err)
	}
	if cl := db.ConnectionModelClearance(ctx, conn.ID, "claude-fable-5"); cl != model.SensitivityInternal {
		t.Fatalf("override not stored: %q", cl)
	}

	// A refresh re-discovers the same models with no clearance set — the override must persist.
	if err := db.ReplaceConnectionModels(ctx, conn.ID, discovered); err != nil {
		t.Fatal(err)
	}
	if cl := db.ConnectionModelClearance(ctx, conn.ID, "claude-fable-5"); cl != model.SensitivityInternal {
		t.Fatalf("override lost across refresh: %q", cl)
	}
	// A model with no override inherits (empty).
	if cl := db.ConnectionModelClearance(ctx, conn.ID, "claude-sonnet-5"); cl != "" {
		t.Fatalf("un-overridden model should inherit (empty), got %q", cl)
	}
}
