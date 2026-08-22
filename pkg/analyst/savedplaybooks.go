package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// resolvePlaybook returns a playbook by id — a built-in first, then a user-saved one from the store.
func (svc *Service) resolvePlaybook(ctx context.Context, id string) (Playbook, error) {
	if pb, ok := PlaybookByID(id); ok {
		return pb, nil
	}
	sp, err := svc.g().GetSavedPlaybook(ctx, id)
	if err != nil {
		return Playbook{}, fmt.Errorf("unknown playbook %q", id)
	}
	var steps []PlaybookStep
	if err := json.Unmarshal(sp.Steps, &steps); err != nil {
		return Playbook{}, fmt.Errorf("saved playbook %q is corrupt: %w", id, err)
	}
	return Playbook{ID: sp.ID, Name: sp.Name, Description: sp.Description, Goal: sp.Goal, Steps: steps, MaxConcurrency: sp.MaxConcurrency}, nil
}

// validatePlaybookSteps checks steps reference real profiles (built-in or saved) and only earlier step
// keys (a DAG).
func (svc *Service) validatePlaybookSteps(ctx context.Context, steps []PlaybookStep) error {
	if len(steps) == 0 {
		return errors.New("a playbook needs at least one step")
	}
	keys := map[string]bool{}
	for _, s := range steps {
		if s.Key == "" {
			return errors.New("every step needs a key")
		}
		if keys[s.Key] {
			return fmt.Errorf("duplicate step key %q", s.Key)
		}
		// A gate is a human-approval pause (ADR-0044) — it has no profile or instruction to delegate.
		if s.Gate {
			if s.Profile != "" || s.Instruction != "" {
				return fmt.Errorf("gate step %q must have no profile or instruction", s.Key)
			}
		} else {
			if !svc.profileExists(ctx, s.Profile) {
				return fmt.Errorf("step %q: unknown profile %q", s.Key, s.Profile)
			}
			if s.Instruction == "" {
				return fmt.Errorf("step %q needs an instruction", s.Key)
			}
		}
		for _, d := range s.DependsOn {
			if !keys[d] {
				return fmt.Errorf("step %q depends on %q, which is not an earlier step", s.Key, d)
			}
		}
		keys[s.Key] = true
	}
	return nil
}

// SavePlaybook stores a user-authored playbook after validating it.
func (svc *Service) SavePlaybook(ctx context.Context, name, description, goal string, steps []PlaybookStep, source string) (model.SavedPlaybook, error) {
	if name == "" {
		return model.SavedPlaybook{}, errors.New("a playbook needs a name")
	}
	if err := svc.validatePlaybookSteps(ctx, steps); err != nil {
		return model.SavedPlaybook{}, err
	}
	b, err := json.Marshal(steps)
	if err != nil {
		return model.SavedPlaybook{}, err
	}
	return svc.g().CreateSavedPlaybook(ctx, model.SavedPlaybook{
		Name: name, Description: description, Goal: goal, Steps: b, Source: source,
	})
}

// UpdatePlaybook edits a user-saved playbook in place, keeping its id so schedules and other references stay
// valid (ADR-0019 "composable & editable"). Built-in playbooks are immutable — editing one is rejected.
func (svc *Service) UpdatePlaybook(ctx context.Context, id, name, description, goal string, steps []PlaybookStep) (model.SavedPlaybook, error) {
	if name == "" {
		return model.SavedPlaybook{}, errors.New("a playbook needs a name")
	}
	if _, ok := PlaybookByID(id); ok {
		return model.SavedPlaybook{}, errors.New("built-in playbooks can't be edited; save a copy instead")
	}
	if err := svc.validatePlaybookSteps(ctx, steps); err != nil {
		return model.SavedPlaybook{}, err
	}
	b, err := json.Marshal(steps)
	if err != nil {
		return model.SavedPlaybook{}, err
	}
	return svc.g().UpdateSavedPlaybook(ctx, model.SavedPlaybook{
		ID: id, Name: name, Description: description, Goal: goal, Steps: b,
	})
}

// SavePlaybookFromPlan records a plan's structure (its steps) as a reusable playbook — the record-as-you-go
// path. The run's results/status are dropped; the reusable structure is what's kept.
func (svc *Service) SavePlaybookFromPlan(ctx context.Context, projectID, planID, name, description string) (model.SavedPlaybook, error) {
	plan, err := svc.p(projectID).GetPlan(ctx, planID)
	if err != nil {
		return model.SavedPlaybook{}, err
	}
	steps := make([]PlaybookStep, 0, len(plan.Steps))
	for _, s := range plan.Steps {
		steps = append(steps, PlaybookStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn})
	}
	return svc.SavePlaybook(ctx, name, description, plan.Goal, steps, "plan:"+planID)
}
