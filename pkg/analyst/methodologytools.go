package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/agent"
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
