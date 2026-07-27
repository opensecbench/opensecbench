package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
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
