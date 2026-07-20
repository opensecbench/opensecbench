package analyst

import (
	"context"
	"errors"
	"io"

	"github.com/opensecbench/opensecbench/pkg/agent"
)

// Corpus read tools (ADR-0020). The Analyst can already read source and traffic; these open the rest of
// the ingested evidence — documents, emails, chat logs, notes (ContextItem bytes in the CAS) — and
// full knowledge-base entries. list_context/get_kb_entry are metadata/knowledge reads; read_context
// returns document content and is egress-gated (ingested corpus is treated as private by default,
// see service.executeFor).

const maxContextBytes = 96 * 1024

// listContext lists the project's ingested corpus (metadata only), optionally filtered by type.
func listContext(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "list_context")
	if err != nil {
		return "", err
	}
	items, err := deps.p().ListContextItemsByProject(ctx, projectID)
	if err != nil {
		return "", err
	}
	typ := stringArg(call, "type")
	type row struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Name string `json:"name"`
	}
	out := make([]row, 0, len(items))
	for _, it := range items {
		if typ != "" && it.Type != typ {
			continue
		}
		out = append(out, row{ID: it.ID, Type: it.Type, Name: it.Name})
	}
	return jsonify(out, nil)
}

// readContext returns the text content of one ingested context item.
func readContext(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "read_context")
	if err != nil {
		return "", err
	}
	id := stringArg(call, "id")
	if id == "" {
		return "", errors.New("read_context requires 'id'")
	}
	ci, err := deps.p().GetContextItem(ctx, id)
	if err != nil {
		return "", err
	}
	if ci.ProjectID != projectID {
		return "", errors.New("context item belongs to a different project")
	}
	if deps.Blobs == nil {
		return "", errors.New("context store unavailable")
	}
	art, err := deps.p().GetArtifact(ctx, ci.ArtifactID)
	if err != nil {
		return "", err
	}
	rc, err := deps.Blobs.Open(art.SHA256)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxContextBytes+1))
	if err != nil {
		return "", err
	}
	truncated := len(data) > maxContextBytes
	if truncated {
		data = data[:maxContextBytes]
	}
	// Binary documents (PDF, docx, …) can't be inlined usefully yet — return metadata, not bytes.
	if !isProbablyText(data) {
		return jsonify(map[string]any{
			"name": ci.Name, "type": ci.Type, "media_type": art.MediaType, "bytes": art.Size,
			"note": "binary document; text extraction not yet supported — read a text export instead",
		}, nil)
	}
	return jsonify(map[string]any{
		"name": ci.Name, "type": ci.Type, "media_type": art.MediaType,
		"content": string(data), "truncated": truncated,
	}, nil)
}

// getKBEntry returns a full knowledge-base entry by id (body included — search/draft don't give the body
// back). KB is target-anchored knowledge, reusable across engagements, so it is not project-scoped.
func getKBEntry(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	id := stringArg(call, "id")
	if id == "" {
		return "", errors.New("get_kb_entry requires 'id'")
	}
	return jsonify(deps.g().GetKBEntry(ctx, id))
}
