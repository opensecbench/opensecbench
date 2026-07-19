package analyst

import (
	"context"

	"github.com/opensecbench/opensecbench/pkg/agent"
)

// listKB returns the current project's knowledge-base entries so the agent can see what durable knowledge
// already exists (and update rather than duplicate) when compiling org/target knowledge (ADR-0040).
func listKB(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "list_kb")
	if err != nil {
		return "", err
	}
	entries, err := deps.Store.ListKBByProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	kindFilter := stringArg(call, "kind")
	type row struct {
		ID          string `json:"id"`
		TargetID    string `json:"target_id"`
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		ReviewState string `json:"review_state"`
	}
	out := make([]row, 0, len(entries))
	for _, e := range entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		out = append(out, row{e.ID, e.TargetID, e.Kind, e.Title, e.ReviewState})
	}
	return jsonify(out, nil)
}
