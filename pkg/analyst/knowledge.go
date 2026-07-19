package analyst

import (
	"context"
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/dossier"
)

// getDossier returns the consolidated "what we know about this system" view — the project's inherited KB
// grouped by kind (ADR-0042). The agent reads this first to know how the target is set up.
func getDossier(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "get_dossier")
	if err != nil {
		return "", err
	}
	entries, err := deps.Store.ListKBByProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	subject := "this project"
	if p, err := deps.Store.GetProject(ctx, projectID); err == nil && p.Name != "" {
		subject = p.Name
	}
	return jsonify(dossier.Assemble(subject, entries, time.Now()), nil)
}

// projectOrg resolves the organization to anchor org-scoped knowledge to (ADR-0041): the current project's
// organization, or — failing that — the given target's organization. Returns "" if neither has one.
func (deps ExecDeps) projectOrg(ctx context.Context, targetID string) string {
	if deps.ProjectID != "" {
		if p, err := deps.Store.GetProject(ctx, deps.ProjectID); err == nil && p.OrganizationID != nil && *p.OrganizationID != "" {
			return *p.OrganizationID
		}
	}
	if targetID != "" {
		if t, err := deps.Store.GetTarget(ctx, targetID); err == nil && t.OrganizationID != nil && *t.OrganizationID != "" {
			return *t.OrganizationID
		}
	}
	return ""
}

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
	now := time.Now()
	type row struct {
		ID          string `json:"id"`
		TargetID    string `json:"target_id"`
		Kind        string `json:"kind"`
		Title       string `json:"title"`
		ReviewState string `json:"review_state"`
		Stale       bool   `json:"stale,omitempty"` // confirmed but aged — verify_kb_entry to refresh (ADR-0043)
	}
	out := make([]row, 0, len(entries))
	for _, e := range entries {
		if kindFilter != "" && e.Kind != kindFilter {
			continue
		}
		stale := e.ReviewState == "confirmed" && dossier.IsStale(e.Kind, e.LastVerifiedAt, now)
		out = append(out, row{e.ID, e.TargetID, e.Kind, e.Title, e.ReviewState, stale})
	}
	return jsonify(out, nil)
}

// verifyKBEntry bumps a known fact's freshness — "still true as of now" (ADR-0043). The agent calls this
// when it re-observes something already in the knowledge base instead of drafting a duplicate. It only
// refreshes the timestamp; it does not confirm drafts (humans do that), so the review gate is preserved.
func verifyKBEntry(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	id := stringArg(call, "id")
	if id == "" {
		return "", errors.New("verify_kb_entry requires 'id' (from get_dossier or list_kb)")
	}
	if err := deps.Store.VerifyKBEntry(ctx, id); err != nil {
		return "", err
	}
	e, err := deps.Store.GetKBEntry(ctx, id)
	if err != nil {
		return "", err
	}
	return jsonify(e, nil)
}
