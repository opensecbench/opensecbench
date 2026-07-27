package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// saveMethodology authors a methodology pack into the shared catalog (ADR-0055), giving the agent the same
// pack-authoring capability a human has in the catalog UI. It reuses the exact validate → persist → register
// path the HTTP handler uses, so an agent-authored pack is indistinguishable from a human-authored one and is
// immediately adoptable/coverable. Reversible: a human can edit or delete the pack afterward.
func saveMethodology(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	if deps.Methods == nil {
		return "", errors.New("methodology registry unavailable in this run")
	}
	if deps.g() == nil {
		return "", errors.New("catalog store unavailable")
	}

	var items []methodology.Item
	if raw := strings.TrimSpace(stringArg(call, "items")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &items); err != nil {
			return "", fmt.Errorf(`'items' must be a JSON array of {title,objective,procedure,standards,suggested_capabilities}: %w`, err)
		}
	}
	m := methodology.Methodology{
		ID:       stringArg(call, "id"),
		Title:    stringArg(call, "title"),
		Tech:     stringArg(call, "tech"),
		Version:  stringArg(call, "version"),
		Keywords: stringsArg(call, "keywords"),
		Items:    items,
	}
	methodology.Normalize(&m)
	if err := methodology.Validate(m); err != nil {
		return "", err
	}
	if err := methodology.CheckItemCollisions(deps.Methods, m); err != nil {
		return "", err
	}

	// Editable iff it already has a saved row; a pack that's in the registry but unsaved is a built-in or
	// extension pack and is immutable — same rule the HTTP handlers enforce.
	_, savedErr := deps.g().GetSavedMethodology(ctx, m.ID)
	isSaved := savedErr == nil
	_, inRegistry := deps.Methods.Get(m.ID)

	m.Builtin = false // never persist the transient UI flag
	data, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	row := model.SavedMethodology{ID: m.ID, Title: m.Title, Data: data}

	action := "created"
	if isSaved {
		if _, err := deps.g().UpdateSavedMethodology(ctx, row); err != nil {
			return "", err
		}
		action = "updated"
	} else {
		if inRegistry {
			return "", fmt.Errorf("%q is a built-in or extension pack and can't be edited; use a different title to create your own", m.ID)
		}
		if _, err := deps.g().CreateSavedMethodology(ctx, row); err != nil {
			return "", err
		}
	}
	deps.Methods.Register(m)

	return jsonify(map[string]any{"id": m.ID, "title": m.Title, "items": len(m.Items), "action": action}, nil)
}

const methodologyConvertSystem = `You convert a security tester's free-form checklist into a structured "methodology pack".
Respond with ONLY a JSON object (no prose, no markdown, no code fences) of exactly this shape:
{"title":"<pack title>","tech":"<one short tag: web|api|mobile|auth|cloud|network|custom>","keywords":["..."],
 "items":[{"title":"<the check>","objective":"<what this check confirms>","procedure":"<how to test it>",
 "standards":["OWASP ASVS V4","CWE-89"],"suggested_capabilities":["semgrep"]}]}
Rules:
- Create one item per distinct check in the checklist. Preserve the tester's intent and wording.
- objective/procedure: infer concise text from the heading if the checklist is terse; use "" if truly unknown.
- standards: include only references actually present or unambiguous (OWASP/CWE/RFC); otherwise use [].
- suggested_capabilities: choose ONLY from this list when clearly relevant, otherwise []: %s
- Stay faithful to the input — never invent checks or a pack unrelated to what was pasted.`

// ConvertChecklist turns a tester's pasted free-form checklist into a structured methodology pack via the LLM
// (ADR-0055) WITHOUT persisting it — the caller reviews it in the editor and saves through the normal create
// path. This is the on-ramp that lets teams keep their loose text checklists yet get the structured model that
// coverage tracking needs. Item/pack ids are cleared so they're derived fresh on save.
func (svc *Service) ConvertChecklist(ctx context.Context, text, titleHint string) (methodology.Methodology, error) {
	if svc.provider == nil {
		return methodology.Methodology{}, errors.New("no LLM provider configured")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return methodology.Methodology{}, errors.New("no checklist text provided")
	}
	tgt := svc.targetForTag(ctx, "cheap") // structuring is a cheap task; route it to the cheap model tag
	user := text
	if h := strings.TrimSpace(titleHint); h != "" {
		user = "Suggested pack title: " + h + "\n\nChecklist:\n" + text
	}
	resp, err := tgt.Provider.Complete(ctx, llm.CompletionRequest{
		Model:     tgt.SessionModel,
		MaxTokens: 4000,
		Messages: []llm.Message{
			{Role: "system", Content: fmt.Sprintf(methodologyConvertSystem, svc.capabilityHint())},
			{Role: "user", Content: user},
		},
	})
	if err != nil {
		return methodology.Methodology{}, err
	}
	raw := extractJSONObject(resp.Text)
	if raw == "" {
		return methodology.Methodology{}, errors.New("the model did not return a methodology; try again or add more detail")
	}
	var m methodology.Methodology
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return methodology.Methodology{}, fmt.Errorf("could not parse the model's methodology JSON: %w", err)
	}
	if strings.TrimSpace(m.Title) == "" {
		m.Title = strings.TrimSpace(titleHint)
	}
	// Always draft as a new pack: clear ids so Normalize derives fresh, collision-free ones on save.
	m.ID = ""
	for i := range m.Items {
		m.Items[i].ID = ""
	}
	methodology.Normalize(&m)
	m.Builtin = false
	return m, nil
}

// capabilityHint lists the registered capability ids so the converter only suggests capabilities that exist.
func (svc *Service) capabilityHint() string {
	if svc.engine == nil {
		return "(none available — use [])"
	}
	ms := svc.engine.Registry().Manifests()
	ids := make([]string, 0, len(ms))
	for _, m := range ms {
		ids = append(ids, m.ID)
	}
	if len(ids) == 0 {
		return "(none available — use [])"
	}
	return strings.Join(ids, ", ")
}
