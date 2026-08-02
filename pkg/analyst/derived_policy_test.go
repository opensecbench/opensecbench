package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestDerivedTierPerProjectAndMode covers ADR-0065 phase 1: the derived-artifact egress tier is resolved
// per-project (project setting wins over the global default seed), and "inherit" mode forces the top tier.
// Uses a split manager so the project and global databases are genuinely distinct.
func TestDerivedTierPerProjectAndMode(t *testing.T) {
	ctx := context.Background()
	m, err := store.OpenManager(t.TempDir(), migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	proj, err := m.CreateProject(ctx, store.NewProject{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	g := m.Global() // per-project policy is namespaced in the global settings table

	svc := &Service{mgr: m}
	sc := svc.scale(ctx)

	// Nothing set → top tier (private-by-default), so behavior is unchanged until configured.
	if got := svc.derivedTier(ctx, proj.ID, sc); got != sc.Max() {
		t.Fatalf("default = %q, want top tier %q", got, sc.Max())
	}

	// Global default seed is used when the project has no override.
	if err := g.SetSetting(ctx, DerivedEgressTierSetting, model.SensitivityInternal); err != nil {
		t.Fatal(err)
	}
	if got := svc.derivedTier(ctx, proj.ID, sc); got != model.SensitivityInternal {
		t.Fatalf("global-default fallback = %q, want internal", got)
	}

	// A per-project tier overrides the global default.
	if err := g.SetSetting(ctx, DerivedTierKey(proj.ID), model.SensitivityOpenSource); err != nil {
		t.Fatal(err)
	}
	if got := svc.derivedTier(ctx, proj.ID, sc); got != model.SensitivityOpenSource {
		t.Fatalf("per-project override = %q, want open_source", got)
	}

	// "inherit" mode ignores the tier and treats derived artifacts as the top tier.
	if err := g.SetSetting(ctx, DerivedModeKey(proj.ID), DerivedModeInherit); err != nil {
		t.Fatal(err)
	}
	if got := svc.derivedTier(ctx, proj.ID, sc); got != sc.Max() {
		t.Fatalf("inherit mode = %q, want top tier %q", got, sc.Max())
	}
}
