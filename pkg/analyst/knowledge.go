package analyst

import (
	"context"
	"errors"
	"strings"
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
	entries, err := deps.Mgr.ListKBForProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	subject := "this project"
	if p, err := deps.Mgr.GetProject(ctx, projectID); err == nil && p.Name != "" {
		subject = p.Name
	}
	return jsonify(dossier.Assemble(subject, entries, time.Now()), nil)
}

// projectOrg resolves the organization to anchor org-scoped knowledge to (ADR-0041): the current project's
// organization, or — failing that — the given target's organization. Returns "" if neither has one.
func (deps ExecDeps) projectOrg(ctx context.Context, targetID string) string {
	if deps.ProjectID != "" {
		if p, err := deps.Mgr.GetProject(ctx, deps.ProjectID); err == nil && p.OrganizationID != nil && *p.OrganizationID != "" {
			return *p.OrganizationID
		}
	}
	if targetID != "" {
		if t, err := deps.g().GetTarget(ctx, targetID); err == nil && t.OrganizationID != nil && *t.OrganizationID != "" {
			return *t.OrganizationID
		}
	}
	return ""
}

// projectGroup resolves the team/group to anchor group-scoped knowledge to (ADR-0041): the current project's
// group. Returns "" if the project belongs to no team.
func (deps ExecDeps) projectGroup(ctx context.Context) string {
	if deps.ProjectID != "" {
		if p, err := deps.Mgr.GetProject(ctx, deps.ProjectID); err == nil && p.GroupID != nil && *p.GroupID != "" {
			return *p.GroupID
		}
	}
	return ""
}

// updateKBEntryTool edits an existing entry's title/body directly — parity with the human's edit
// (ADR-0053). Tags are preserved; best-effort re-index for semantic retrieval.
func updateKBEntryTool(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	id, title, body := stringArg(call, "id"), stringArg(call, "title"), stringArg(call, "body")
	if id == "" || title == "" {
		return "", errors.New("update_kb_entry requires 'id' and 'title'")
	}
	existing, err := deps.g().GetKBEntry(ctx, id)
	if err != nil {
		return "", err
	}
	if err := deps.g().UpdateKBEntry(ctx, id, title, body, existing.Tags); err != nil {
		return "", err
	}
	e, err := deps.g().GetKBEntry(ctx, id)
	if err != nil {
		return "", err
	}
	if deps.ProjectID != "" && deps.Indexer != nil && deps.Indexer.Available() {
		_ = deps.Indexer.IndexKBEntry(ctx, deps.ProjectID, e)
	}
	return jsonify(e, nil)
}

// searchKB keyword-matches the project's inherited KB (title + body) — a quick lookup that complements the
// semantic search_corpus.
func searchKB(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "search_kb")
	if err != nil {
		return "", err
	}
	q := strings.ToLower(strings.TrimSpace(stringArg(call, "q")))
	if q == "" {
		return "", errors.New("search_kb requires 'q'")
	}
	limit := intArg(call, "limit")
	if limit <= 0 {
		limit = 15
	}
	entries, err := deps.Mgr.ListKBForProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	type row struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		Scope string `json:"scope"`
		Title string `json:"title"`
		Body  string `json:"body,omitempty"`
	}
	out := make([]row, 0, limit)
	for _, e := range entries {
		if strings.Contains(strings.ToLower(e.Title+" "+e.Body), q) {
			out = append(out, row{e.ID, e.Kind, e.Scope, e.Title, truncate(e.Body, 400)})
			if len(out) >= limit {
				break
			}
		}
	}
	return jsonify(out, nil)
}

// listKB returns the current project's knowledge-base entries so the agent can see what durable knowledge
// already exists (and update rather than duplicate) when compiling org/target knowledge (ADR-0040).
func listKB(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "list_kb")
	if err != nil {
		return "", err
	}
	entries, err := deps.Mgr.ListKBForProject(ctx, projectID)
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
	if err := deps.g().VerifyKBEntry(ctx, id); err != nil {
		return "", err
	}
	e, err := deps.g().GetKBEntry(ctx, id)
	if err != nil {
		return "", err
	}
	return jsonify(e, nil)
}
