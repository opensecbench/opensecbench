package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// TestDerivedArtifactEgressCarveOut verifies ADR-0064: derived scan output (findings) is private-by-default
// but becomes egress-eligible when the derived tier is lowered, while raw-source reads stay gated — so an
// engagement can send scanner output to an external model with the source it came from never leaving.
func TestDerivedArtifactEgressCarveOut(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t)
	proj, err := db.CreateProject(ctx, store.NewProject{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateFinding(ctx, store.NewFinding{Title: "SQLi", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	blobs, err := cas.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := &Service{mgr: store.NewCombinedManager(db), casr: cas.Fixed(blobs)}

	ext := &llm.AnthropicProvider{} // external destination
	listFindings := agent.ToolCall{Tool: "list_findings"}
	readFile := agent.ToolCall{Tool: "read_file"} // raw source; no asset arg ⇒ private-by-default

	// run returns the tool result; over-clearance content is withheld (ADR-0065) — a successful call with
	// a {"withheld":true} marker, not an error.
	run := func(call agent.ToolCall, clearance string) string {
		out, err := svc.executeFor(proj.ID, ext, clearance)(ctx, call)
		if err != nil {
			t.Fatalf("%s returned an error (should withhold, not error): %v", call.Tool, err)
		}
		return out
	}

	// Default (top tier): list_findings is withheld from an open-source-cleared external provider.
	if out := run(listFindings, model.SensitivityOpenSource); !strings.Contains(out, "withheld") {
		t.Fatalf("list_findings should be withheld by default, got %s", out)
	}

	// run_capability is DERIVED now (its summary), not asset-gated: withheld by default (derived tier is
	// top) — so its egress follows the derived-sharing policy, not the asset's sensitivity (ADR-0065).
	runCap := agent.ToolCall{Tool: "run_capability", Args: map[string]any{"capability": "semgrep", "asset": "x"}}
	if out := run(runCap, model.SensitivityOpenSource); !strings.Contains(out, "withheld") {
		t.Fatalf("run_capability should be derived-gated and withheld by default, got %s", out)
	}

	// Carve-out: lower the derived tier to open-source ⇒ scan output may now reach that provider.
	if err := db.SetSetting(ctx, DerivedEgressTierSetting, model.SensitivityOpenSource); err != nil {
		t.Fatal(err)
	}
	if out := run(listFindings, model.SensitivityOpenSource); strings.Contains(out, "withheld") {
		t.Fatalf("list_findings should be allowed after lowering the derived tier, got %s", out)
	}

	// The carve-out must NOT open raw source: read_file stays private-by-default and withheld.
	if out := run(readFile, model.SensitivityOpenSource); !strings.Contains(out, "withheld") {
		t.Fatalf("read_file (raw source) must stay withheld even with the derived carve-out, got %s", out)
	}

	// A local provider is never gated regardless of tier — real result, no withheld marker.
	if out, err := svc.executeFor(proj.ID, &llm.MockProvider{}, model.SensitivityOpenSource)(ctx, listFindings); err != nil || strings.Contains(out, "withheld") {
		t.Fatalf("local provider list_findings should return real data: out=%s err=%v", out, err)
	}
}

// TestListAssetsScopedAndDLPEvent covers ADR-0065 phases 3b + 4: list_assets runs and returns only the
// assets the destination is cleared for (per-item, over-clearance never returned), and a withheld read is
// recorded as a DLP event so the boundary is auditable.
func TestListAssetsScopedAndDLPEvent(t *testing.T) {
	ctx := context.Background()
	db := storetest.New(t)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	pub, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/oss", Sensitivity: model.SensitivityOpenSource})
	if err != nil {
		t.Fatal(err)
	}
	priv, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/secret", Sensitivity: model.SensitivityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(t.TempDir())
	svc := &Service{mgr: store.NewCombinedManager(db), casr: cas.Fixed(blobs)}
	ext := &llm.AnthropicProvider{}

	// list_assets is self-scoped: an open-source-cleared destination sees only the open-source asset.
	out, err := svc.executeFor(proj.ID, ext, model.SensitivityOpenSource)(ctx, agent.ToolCall{Tool: "list_assets"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, pub.ID) {
		t.Fatalf("list_assets should include the open-source asset, got %s", out)
	}
	if strings.Contains(out, priv.ID) || !strings.Contains(out, "withheld") {
		t.Fatalf("list_assets should hide the private asset and note it withheld, got %s", out)
	}

	// A withheld read records a DLP event (auditable boundary).
	_, _ = svc.executeFor(proj.ID, ext, model.SensitivityOpenSource)(ctx, listFindingsCall())
	events, err := db.ListDLPEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	var audited bool
	for _, e := range events {
		if e.Kind == "egress" && e.Blocked {
			audited = true
		}
	}
	if !audited {
		t.Fatalf("expected a DLP egress-withheld event, got %+v", events)
	}
}

func listFindingsCall() agent.ToolCall { return agent.ToolCall{Tool: "list_findings"} }
