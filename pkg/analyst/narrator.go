package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/report"
)

// Narrate authors report narrative from a grounded snapshot (ADR-0045): an executive summary plus per-finding
// impact and remediation. It is a single, bounded LLM call (not an agent loop) over the exact reportable
// finding set the Builder assembled, so the prose can never introduce findings that aren't evidence-backed —
// it only explains the ones already there. Implements report.Narrator.
//
// Egress note: narration sends finding titles/descriptions + evidence details to the provider. It reuses the
// service's active provider; the caller gates it (a restricted engagement / no provider ⇒ don't narrate).
// reportNarratorTag is the report-writer profile's model tag: narration is a cheap summarization, so it
// routes to whatever model that tag maps to (ADR-0021) rather than the (possibly expensive) active model.
const reportNarratorTag = "cheap"

func (svc *Service) Narrate(ctx context.Context, d report.Data, audience string) (report.Narrative, error) {
	if svc.provider == nil {
		return report.Narrative{}, errors.New("no LLM provider configured")
	}
	tgt := svc.targetForTag(ctx, reportNarratorTag)
	// Narration sends finding titles/descriptions + evidence to the model (private-by-default egress).
	if !svc.clearedForPrivate(ctx, d.Project.ID, tgt) {
		return report.Narrative{}, fmt.Errorf("report narration blocked by data-egress policy: %q is cleared only for %s, but narration would send finding content; use a local provider (e.g. ollama) or raise the destination's clearance", tgt.Provider.Name(), svc.scale(ctx).Label(tgt.Clearance))
	}
	resp, err := tgt.Provider.Complete(ctx, llm.CompletionRequest{
		Model:     tgt.SessionModel,
		MaxTokens: 4000,
		Messages: []llm.Message{
			{Role: "system", Content: narratorSystem + audienceGuidance(audience)},
			{Role: "user", Content: buildNarratorPrompt(d)},
		},
	})
	if err != nil {
		return report.Narrative{}, err
	}
	// Attribute the narration tokens like any other agent run (ADR-0021).
	svc.recordDelegateUsage(ctx, d.Project.ID, "report-writer", tgt, resp.InputTokens, resp.OutputTokens)
	return parseNarrative(resp.Text)
}

const narratorSystem = "You are a senior security report writer. You are given a security engagement's " +
	"confirmed, evidence-backed findings and its coverage. Write clear, precise, audience-aware report " +
	"narrative. Ground every statement in the supplied findings and evidence — never invent findings, CVEs, " +
	"or facts not present. Be concise and concrete.\n\n" +
	"Respond with ONLY a JSON object, no prose around it, of the form:\n" +
	"{\"executive_summary\": \"2-4 short paragraphs on the engagement's outcome, risk posture, and themes\", " +
	"\"findings\": [{\"id\": \"<finding id>\", \"impact\": \"what an attacker gains / business risk\", " +
	"\"remediation\": \"specific, actionable fix guidance\"}]}\n" +
	"Include one findings entry per finding id supplied. Keep impact and remediation to a few sentences each."

// audienceGuidance tailors the narrator's tone to who reads the report (ADR-0045).
func audienceGuidance(audience string) string {
	switch audience {
	case report.AudienceExecutive:
		return "\n\nAudience: EXECUTIVE / business stakeholders. Lead with business risk and priorities; " +
			"minimize jargon; frame impact in terms of what's at stake; keep remediation high-level (what to do, " +
			"not code)."
	default:
		return "\n\nAudience: TECHNICAL / engineers. Be precise and concrete; name the mechanism; give specific, " +
			"actionable remediation (configs, code-level fixes, controls)."
	}
}

// buildNarratorPrompt renders the grounded snapshot into a compact prompt. Evidence is trimmed so a large
// engagement stays within a sane prompt size.
func buildNarratorPrompt(d report.Data) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Engagement: %s\n", d.Project.Name)
	if m := d.Methodology.Summary; m.Total > 0 {
		fmt.Fprintf(&b, "Methodology coverage: %d%% (%d/%d covered)\n", m.CoveredPct, m.Covered, m.Total)
	}
	fmt.Fprintf(&b, "Reportable findings (%d):\n", len(d.Findings))
	for _, f := range d.Findings {
		fmt.Fprintf(&b, "\n- id: %s\n  title: %s\n  severity: %s\n", f.ID, f.Title, f.Severity)
		if f.CWE != "" {
			fmt.Fprintf(&b, "  cwe: %s\n", f.CWE)
		}
		if strings.TrimSpace(f.Description) != "" {
			fmt.Fprintf(&b, "  description: %s\n", oneLine(f.Description, 500))
		}
		for i, ev := range f.Evidence {
			if i >= 4 { // cap evidence per finding in the prompt
				fmt.Fprintf(&b, "  evidence: (+%d more)\n", len(f.Evidence)-i)
				break
			}
			loc := ev.Location
			if loc != "" {
				loc = " @ " + loc
			}
			fmt.Fprintf(&b, "  evidence: %s%s — %s\n", ev.Title, loc, oneLine(ev.Detail, 240))
		}
	}
	return b.String()
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

// parseNarrative tolerantly extracts the JSON object from the model's reply (models sometimes wrap it in
// prose or a code fence) and maps it into a report.Narrative keyed by finding id.
func parseNarrative(text string) (report.Narrative, error) {
	raw := extractJSONObject(text)
	if raw == "" {
		return report.Narrative{}, fmt.Errorf("narrator returned no JSON object")
	}
	var parsed struct {
		ExecutiveSummary string                    `json:"executive_summary"`
		Findings         []report.FindingNarrative `json:"findings"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return report.Narrative{}, fmt.Errorf("narrator JSON: %w", err)
	}
	n := report.Narrative{ExecutiveSummary: strings.TrimSpace(parsed.ExecutiveSummary), Findings: map[string]report.FindingNarrative{}}
	for _, f := range parsed.Findings {
		if f.ID != "" {
			n.Findings[f.ID] = f
		}
	}
	return n, nil
}

// extractJSONObject returns the substring from the first '{' to the last '}', tolerating code fences and
// surrounding prose. Empty if no braces are found.
func extractJSONObject(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j < 0 || j < i {
		return ""
	}
	return s[i : j+1]
}
