package analyst

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// behavioralFraming maps a reserved context tag (model.BehavioralContextTags) to the directive the agent is
// given for a note carrying it. These turn an analyst label into standing guidance the agent must honor.
var behavioralFraming = map[string]string{
	model.CtxTagOutOfScope: "OUT OF SCOPE — do not probe, test, target, or act on the subject of this note.",
	model.CtxTagConstraint: "HARD CONSTRAINT — you must honor this throughout the engagement.",
	model.CtxTagPriority:   "PRIORITY — the analyst wants you to focus here.",
	model.CtxTagHypothesis: "HYPOTHESIS — a lead to pursue; try to confirm or refute it.",
}

const maxNoteInjectBytes = 8 << 10 // cap per note folded into the run-start context; notes are short by design

// systemPromptFor is the profile's system prompt plus, when the project has any, the run-start analyst-notes
// preamble (pinned + behaviorally-tagged context). Used wherever a fresh agent run is seeded (main thread and
// delegated sub-agents) so both honor the analyst's standing guidance.
func (svc *Service) systemPromptFor(ctx context.Context, projectID, base string, prov llm.Provider, clearance string) string {
	if pre := svc.contextNotesPreamble(ctx, projectID, prov, clearance); pre != "" {
		return base + "\n\n" + pre
	}
	return base
}

// contextNotesPreamble builds the run-start injection of analyst context the agent must be aware of without
// having to look: every pinned item plus every item carrying a reserved behavioral tag (out-of-scope,
// constraint, priority, hypothesis). Everything else stays pull-only via list_context/read_context.
//
// Note bodies are private ingested corpus, so they honor the same egress ceiling as read_context: against an
// external provider without private-egress permission, only a redacted signal (behavioral tags + counts, no
// names or bodies) is injected — the cue still lands without leaking the note text. Returns "" when there is
// nothing to inject.
func (svc *Service) contextNotesPreamble(ctx context.Context, projectID string, prov llm.Provider, clearance string) string {
	if projectID == "" {
		return ""
	}
	items, err := svc.p(projectID).ListContextItemsByProject(ctx, projectID)
	if err != nil {
		return ""
	}
	var inject []model.ContextItem
	for _, it := range items {
		if it.Pinned || hasBehavioralTag(it.Tags) {
			inject = append(inject, it)
		}
	}
	if len(inject) == 0 {
		return ""
	}

	// Same egress gate read_context resolves: a local provider is never a risk; an external one honors the
	// destination's data clearance (note bodies are private ingested corpus), tightened to open-source only
	// for a restricted engagement (ADR-0051).
	external := prov != nil && !llm.IsLocal(prov)
	sc := svc.scale(ctx)
	allowPrivate := sc.Allows(clearance, sc.Max())
	if external {
		if eng, err := svc.p(projectID).GetEngagement(ctx, projectID); err == nil && eng.DataClass == model.DataRestricted {
			allowPrivate = false
		}
	}

	var b strings.Builder
	b.WriteString("## Analyst context notes\n")
	b.WriteString("The analyst flagged these for you to honor without being asked. Treat them as standing guidance for this engagement.\n")

	if external && !allowPrivate {
		// Redacted: the bodies are private and this provider is external — emit only the behavioral signal.
		counts := map[string]int{}
		for _, it := range inject {
			for _, t := range it.Tags {
				if model.IsBehavioralTag(t) {
					counts[strings.ToLower(strings.TrimSpace(t))]++
				}
			}
		}
		keys := make([]string, 0, len(counts))
		for t := range counts {
			keys = append(keys, t)
		}
		sort.Strings(keys)
		for _, t := range keys {
			fmt.Fprintf(&b, "- %d note(s) tagged %q: %s\n", counts[t], t, behavioralFraming[t])
		}
		fmt.Fprintf(&b, "- %d pinned/flagged note(s) total. Their content is withheld from this external provider by data-egress policy; read them with read_context via a local provider (e.g. ollama) to see the detail.\n", len(inject))
		return b.String()
	}

	for _, it := range inject {
		var directives []string
		for _, t := range model.BehavioralContextTags {
			if containsFold(it.Tags, t) {
				directives = append(directives, behavioralFraming[t])
			}
		}
		label := "PINNED — keep this in mind."
		if len(directives) > 0 {
			label = strings.Join(directives, " ")
		}
		fmt.Fprintf(&b, "\n### %s\n%s\n", it.Name, label)
		if extra := freeformTags(it.Tags); len(extra) > 0 {
			fmt.Fprintf(&b, "tags: %s\n", strings.Join(extra, ", "))
		}
		if body := svc.contextItemText(ctx, projectID, it); body != "" {
			b.WriteString(body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// contextItemText loads a context item's text from the CAS, trimmed and capped for injection. Returns "" for
// a missing/binary/unreadable item — a pinned binary doc is left for the agent to read_context on demand.
func (svc *Service) contextItemText(ctx context.Context, projectID string, it model.ContextItem) string {
	if it.ArtifactID == "" {
		return ""
	}
	art, err := svc.p(projectID).GetArtifact(ctx, it.ArtifactID)
	if err != nil {
		return ""
	}
	rc, err := svc.casFor(projectID).Open(art.SHA256)
	if err != nil {
		return ""
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, maxNoteInjectBytes+1))
	if err != nil || !isProbablyText(data) {
		return ""
	}
	if len(data) > maxNoteInjectBytes {
		return strings.TrimSpace(string(data[:maxNoteInjectBytes])) + "\n…(truncated)"
	}
	return strings.TrimSpace(string(data))
}

func hasBehavioralTag(tags []string) bool {
	for _, t := range tags {
		if model.IsBehavioralTag(t) {
			return true
		}
	}
	return false
}

// freeformTags returns the non-behavioral tags (the reserved ones are already surfaced as directives).
func freeformTags(tags []string) []string {
	var out []string
	for _, t := range tags {
		if !model.IsBehavioralTag(t) {
			out = append(out, t)
		}
	}
	return out
}

func containsFold(tags []string, want string) bool {
	for _, t := range tags {
		if strings.EqualFold(strings.TrimSpace(t), want) {
			return true
		}
	}
	return false
}
