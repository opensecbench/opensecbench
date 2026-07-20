package analyst

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/report"
)

// generateReport builds a report from the project's confirmed findings and stores it as a durable
// deliverable (ADR-0045) — the artifact an autonomous assessment hands back. It reuses the report builder
// (ADR-0008), so only findings with traceable evidence appear; the agent can't invent content. Markdown is
// the default (no headless browser needed, so it works in any autonomous run); HTML is also supported.
func generateReport(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	projectID, err := requireProject(deps, "generate_report")
	if err != nil {
		return "", err
	}
	if deps.Blobs == nil {
		return "", errors.New("generate_report: no artifact store configured")
	}

	templateID := stringArg(call, "template")
	if templateID == "" {
		templateID = "technical"
	}
	tmpl, ok := report.BuiltIns().Get(templateID)
	if !ok {
		return "", errors.New("generate_report: unknown template " + templateID + " (try technical, executive, compliance, retest)")
	}
	format := report.Format(stringArg(call, "format"))
	switch format {
	case report.FormatMarkdown, report.FormatHTML:
	default:
		format = report.FormatMarkdown // agent-safe default: no browser dependency
	}

	data, err := report.NewBuilder(deps.p()).Build(ctx, projectID, time.Now())
	if err != nil {
		return "", errors.New("generate_report: build: " + err.Error())
	}
	// Author narrative when a Narrator is wired (ADR-0045) — an executive summary + per-finding impact/
	// remediation grounded in these findings. Best-effort: a failure yields the data-only report.
	if deps.Narrator != nil {
		if n, nerr := deps.Narrator.Narrate(ctx, data, report.AudienceFor(tmpl.ID)); nerr == nil {
			data.ApplyNarrative(n)
		}
	}
	rendered, err := tmpl.Render(data, format)
	if err != nil {
		return "", errors.New("generate_report: render: " + err.Error())
	}

	digest, err := deps.Blobs.Put(bytes.NewReader(rendered))
	if err != nil {
		return "", err
	}
	mediaType := "text/markdown; charset=utf-8"
	if format == report.FormatHTML {
		mediaType = "text/html; charset=utf-8"
	}
	art, err := deps.p().CreateArtifact(ctx, model.Artifact{
		SHA256:    digest,
		Size:      int64(len(rendered)),
		Kind:      model.ArtifactOutput,
		Name:      tmpl.ID + "." + string(format),
		MediaType: mediaType,
	})
	if err != nil {
		return "", err
	}
	rep, err := deps.p().CreateReport(ctx, model.Report{
		ProjectID:  projectID,
		TemplateID: tmpl.ID,
		Format:     string(format),
		Title:      tmpl.Title,
		ArtifactID: art.ID,
	})
	if err != nil {
		return "", err
	}
	return jsonify(map[string]any{
		"report_id":   rep.ID,
		"artifact_id": art.ID,
		"template":    tmpl.ID,
		"title":       tmpl.Title,
		"format":      string(format),
		"findings":    data.Summary.Total,
		"narrated":    data.Narrated,
	}, nil)
}
