package analyst

import (
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func createResearchItem(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	pid, err := requireProject(deps, "create_research_item")
	if err != nil {
		return "", err
	}
	itemType := stringArg(call, "type")
	title := stringArg(call, "title")
	if title == "" {
		return "", fmt.Errorf("create_research_item requires 'title'")
	}
	if itemType == "" {
		itemType = "note"
	}
	item, err := deps.p().CreateResearchItem(ctx, store.NewResearchItem{
		ProjectID:  pid,
		Type:       itemType,
		Title:      title,
		Body:       stringArg(call, "body"),
		Status:     stringArg(call, "status"),
		Assessment: stringArg(call, "assessment"),
		CreatedBy:  "agent",
		Tags:       strSliceArg(call, "tags"),
	})
	if err != nil {
		return "", fmt.Errorf("create_research_item: %w", err)
	}
	return jsonify(item, nil)
}

func listResearchItems(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	pid, err := requireProject(deps, "list_research_items")
	if err != nil {
		return "", err
	}
	items, err := deps.p().ListResearchItems(ctx, pid)
	if err != nil {
		return "", fmt.Errorf("list_research_items: %w", err)
	}
	return jsonify(items, nil)
}

func updateResearchItem(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if _, err := requireProject(deps, "update_research_item"); err != nil {
		return "", err
	}
	id := stringArg(call, "id")
	if id == "" {
		return "", fmt.Errorf("update_research_item requires 'id'")
	}
	var upd store.ResearchItemUpdate
	if v, ok := call.Args["title"]; ok {
		s := v.(string)
		upd.Title = &s
	}
	if v, ok := call.Args["body"]; ok {
		s := v.(string)
		upd.Body = &s
	}
	if v, ok := call.Args["status"]; ok {
		s := v.(string)
		upd.Status = &s
	}
	if v, ok := call.Args["assessment"]; ok {
		s := v.(string)
		upd.Assessment = &s
	}
	if v, ok := call.Args["tags"]; ok {
		_ = v
		tags := strSliceArg(call, "tags")
		upd.Tags = &tags
	}
	item, err := deps.p().UpdateResearchItem(ctx, id, upd)
	if err != nil {
		return "", fmt.Errorf("update_research_item: %w", err)
	}
	return jsonify(item, nil)
}
