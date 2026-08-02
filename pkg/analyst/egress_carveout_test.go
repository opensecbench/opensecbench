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
