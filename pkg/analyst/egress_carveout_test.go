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

	blocked := func(call agent.ToolCall, clearance string) error {
		_, err := svc.executeFor(proj.ID, ext, clearance)(ctx, call)
		return err
	}

	// Default (top tier): list_findings is blocked to an open-source-cleared external provider.
	if err := blocked(listFindings, model.SensitivityOpenSource); err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("list_findings should be egress-blocked by default, got %v", err)
	}

	// Carve-out: lower the derived tier to open-source ⇒ scan output may now reach that provider.
	if err := db.SetSetting(ctx, DerivedEgressTierSetting, model.SensitivityOpenSource); err != nil {
		t.Fatal(err)
	}
	if err := blocked(listFindings, model.SensitivityOpenSource); err != nil {
		t.Fatalf("list_findings should be allowed after lowering the derived tier, got %v", err)
	}

	// The carve-out must NOT open raw source: read_file stays private-by-default and blocked.
	if err := blocked(readFile, model.SensitivityOpenSource); err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("read_file (raw source) must stay egress-blocked even with the derived carve-out, got %v", err)
	}

	// A local provider was never gated regardless of tier.
	if _, err := svc.executeFor(proj.ID, &llm.MockProvider{}, model.SensitivityOpenSource)(ctx, listFindings); err != nil {
		t.Fatalf("local provider list_findings should not be blocked: %v", err)
	}
}
