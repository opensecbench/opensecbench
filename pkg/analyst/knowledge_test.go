package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// list_kb surfaces the project's target-anchored KB entries (so the scribe can dedupe), filterable by kind.
func TestListKB(t *testing.T) {
	db := migratedStore(t)
	ctx := context.Background()
	tgt, _ := db.CreateTarget(ctx, "acme", "", nil)
	proj, err := db.CreateProject(ctx, store.NewProject{Name: "p", TargetIDs: []string{tgt.ID}})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = db.CreateKBEntry(ctx, model.KBEntry{TargetID: tgt.ID, Kind: "auth", Title: "OIDC via Keycloak", Origin: model.OriginHuman})
	_, _ = db.CreateKBEntry(ctx, model.KBEntry{TargetID: tgt.ID, Kind: "tech_stack", Title: "nginx + Postgres", Origin: model.OriginHuman})

	exec := Executor(ExecDeps{Store: db, ProjectID: proj.ID})
	out, err := exec(ctx, agent.ToolCall{Tool: "list_kb", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "OIDC via Keycloak") || !strings.Contains(out, "nginx + Postgres") {
		t.Fatalf("list_kb should return the target's entries: %s", out)
	}
	// Filter by kind.
	authOnly, _ := exec(ctx, agent.ToolCall{Tool: "list_kb", Args: map[string]any{"kind": "auth"}})
	if !strings.Contains(authOnly, "OIDC") || strings.Contains(authOnly, "nginx") {
		t.Fatalf("kind filter wrong: %s", authOnly)
	}
}

// The knowledge-scribe compiles knowledge but cannot mutate assessment state or reach out — least privilege
// for the capture loop (ADR-0040).
func TestKnowledgeScribeProfileAndPlaybooks(t *testing.T) {
	p := ProfileByID("knowledge-scribe")
	tools := map[string]bool{}
	for _, tn := range p.Tools {
		tools[tn] = true
	}
	if !tools["draft_kb_entry"] || !tools["list_kb"] {
		t.Fatal("scribe must be able to read + draft KB")
	}
	for _, deny := range []string{"create_finding", "run_capability", "send_request", "web_fetch", "run_code"} {
		if tools[deny] {
			t.Fatalf("scribe should not have %q", deny)
		}
	}
	// The capture-knowledge playbook uses the scribe; onboarding now has a capture step.
	cap, ok := PlaybookByID("capture-knowledge")
	if !ok || len(cap.Steps) == 0 || cap.Steps[0].Profile != "knowledge-scribe" {
		t.Fatalf("capture-knowledge playbook missing or wrong profile: %+v", cap)
	}
	onb, _ := PlaybookByID("onboarding")
	hasCapture := false
	for _, s := range onb.Steps {
		if s.Profile == "knowledge-scribe" {
			hasCapture = true
		}
	}
	if !hasCapture {
		t.Fatal("onboarding should include a knowledge-scribe capture step")
	}
}
