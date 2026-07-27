package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestSaveMethodologyTool(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	reg := methodology.BuiltIns()
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID, Methods: reg})

	// Create a new pack — id + item ids are derived from titles.
	out, err := exec(ctx, agent.ToolCall{Tool: "save_methodology", Args: map[string]any{
		"title": "GraphQL API",
		"items": `[{"title":"Query depth limiting","objective":"Bound query cost"}]`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "graphql-api") || !strings.Contains(out, "created") {
		t.Fatalf("create out = %s", out)
	}
	if _, ok := reg.Get("graphql-api"); !ok {
		t.Fatal("pack not registered after create")
	}
	if _, _, ok := reg.Item("graphql-api/query-depth-limiting"); !ok {
		t.Fatal("item not resolvable after create")
	}
	if _, err := db.GetSavedMethodology(ctx, "graphql-api"); err != nil {
		t.Fatalf("pack not persisted: %v", err)
	}

	// Edit in place by id.
	out, err = exec(ctx, agent.ToolCall{Tool: "save_methodology", Args: map[string]any{
		"id": "graphql-api", "title": "GraphQL API v2",
		"items": `[{"title":"Query depth limiting"},{"title":"Introspection disabled"}]`,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "updated") {
		t.Fatalf("update out = %s", out)
	}
	if p, _ := reg.Get("graphql-api"); p.Title != "GraphQL API v2" || len(p.Items) != 2 {
		t.Fatalf("update not applied to registry: %+v", p)
	}

	// A built-in can't be edited.
	if _, err := exec(ctx, agent.ToolCall{Tool: "save_methodology", Args: map[string]any{
		"id": "web-app", "title": "hijack", "items": `[{"title":"x"}]`,
	}}); err == nil {
		t.Fatal("editing built-in should fail")
	}

	// Item ids that collide with another pack are rejected.
	if _, err := exec(ctx, agent.ToolCall{Tool: "save_methodology", Args: map[string]any{
		"title": "Sneaky", "items": `[{"id":"web-app/xss","title":"steal"}]`,
	}}); err == nil {
		t.Fatal("colliding item id should fail")
	}

	// Malformed items JSON is a clear error, not a panic.
	if _, err := exec(ctx, agent.ToolCall{Tool: "save_methodology", Args: map[string]any{
		"title": "Bad", "items": `not json`,
	}}); err == nil {
		t.Fatal("malformed items should fail")
	}
}

// An agent check carries its methodology item through context, so an observation the sub-agent records
// attaches to that item as evidence — the agent equivalent of the capability path's evidence link (ADR-0056).
func TestCreateObservationLinksMethodologyEvidence(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	// With an item in context, the recorded observation links to it as evidence.
	if _, err := exec(withMethodologyItem(ctx, "web-app/idor"),
		agent.ToolCall{Tool: "create_observation", Args: map[string]any{"title": "IDOR on /orders", "severity": "high"}}); err != nil {
		t.Fatal(err)
	}
	counts, _ := db.CountCoverageEvidence(ctx, projectID)
	if counts["web-app/idor"] != 1 {
		t.Fatalf("observation not linked to item as evidence: %v", counts)
	}

	// Without an item in context, nothing links (an ordinary agent turn).
	if _, err := exec(ctx, agent.ToolCall{Tool: "create_observation", Args: map[string]any{"title": "unrelated"}}); err != nil {
		t.Fatal(err)
	}
	counts2, _ := db.CountCoverageEvidence(ctx, projectID)
	if counts2["web-app/idor"] != 1 {
		t.Fatalf("a non-methodology observation should not link: %v", counts2)
	}
}

func TestConvertChecklist(t *testing.T) {
	ctx := context.Background()
	db, _ := seedProject(t)
	// The model returns methodology JSON (wrapped in a code fence, to exercise the tolerant extractor).
	resp := "```json\n" + `{"title":"GraphQL API","tech":"api","keywords":["graphql"],
		"items":[{"title":"Query depth limiting","objective":"Bound cost"},{"title":"Introspection disabled"}]}` + "\n```"
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{Responses: []string{resp}})

	m, err := svc.ConvertChecklist(ctx, "- limit query depth\n- disable introspection", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "GraphQL API" || m.ID != "graphql-api" {
		t.Fatalf("draft pack ids not derived: %+v", m)
	}
	if len(m.Items) != 2 || m.Items[0].ID != "graphql-api/query-depth-limiting" {
		t.Fatalf("draft items wrong: %+v", m.Items)
	}
	if m.Builtin {
		t.Fatal("draft should not be flagged builtin")
	}

	// No provider ⇒ a clear error, not a panic.
	if _, err := NewService(store.NewCombinedManager(db), nil, nil, "", nil).ConvertChecklist(ctx, "x", ""); err == nil {
		t.Fatal("convert without a provider should fail")
	}
}
