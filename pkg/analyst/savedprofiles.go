package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// resolveProfile returns a profile by id — a built-in first, then a user-saved one; an unknown id falls
// back to the generalist so a run never fails to start on a stale profile reference.
func (svc *Service) resolveProfile(ctx context.Context, id string) Profile {
	if p, ok := builtinProfile(id); ok {
		return p
	}
	if sp, err := svc.g().GetSavedProfile(ctx, id); err == nil {
		var tools []string
		_ = json.Unmarshal(sp.Tools, &tools)
		return Profile{ID: sp.ID, Name: sp.Name, Description: sp.Description, Persona: sp.Persona, Tools: tools}
	}
	return ProfileByID(id) // generalist fallback
}

// profileExists reports whether an id names a built-in or saved profile.
func (svc *Service) profileExists(ctx context.Context, id string) bool {
	if _, ok := builtinProfile(id); ok {
		return true
	}
	_, err := svc.g().GetSavedProfile(ctx, id)
	return err == nil
}

// SaveProfile stores a user-defined profile after validating its persona and tool allow-list.
func (svc *Service) SaveProfile(ctx context.Context, name, description, persona string, tools []string) (model.SavedProfile, error) {
	if name == "" || persona == "" {
		return model.SavedProfile{}, errors.New("a custom agent needs a name and a persona")
	}
	if len(tools) == 0 {
		return model.SavedProfile{}, errors.New("a custom agent needs at least one tool (least privilege)")
	}
	catalog := map[string]bool{}
	for _, t := range Tools() {
		catalog[t.Name] = true
	}
	for _, t := range tools {
		if !catalog[t] {
			return model.SavedProfile{}, fmt.Errorf("unknown tool %q", t)
		}
	}
	b, err := json.Marshal(tools)
	if err != nil {
		return model.SavedProfile{}, err
	}
	return svc.g().CreateSavedProfile(ctx, model.SavedProfile{Name: name, Description: description, Persona: persona, Tools: b})
}
