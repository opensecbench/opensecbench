package analyst

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// StartPlan creates a plan from a playbook and runs it in the background, executing steps in dependency
// order via delegation. It returns the created plan (status running) immediately; the client polls
// GetPlan to watch progress. This is the human-triggered, bounded run — it stops when the DAG is done.
func (svc *Service) StartPlan(ctx context.Context, projectID, playbookID string) (model.Plan, error) {
	if svc.provider == nil {
		return model.Plan{}, errors.New("no LLM provider configured")
	}
	pb, err := svc.resolvePlaybook(ctx, playbookID)
	if err != nil {
		return model.Plan{}, err
	}
	plan := model.Plan{ProjectID: projectID, PlaybookID: pb.ID, Goal: pb.Goal, Status: model.PlanRunning}
	for _, s := range pb.Steps {
		plan.Steps = append(plan.Steps, model.PlanStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn})
	}
	created, err := svc.store.CreatePlan(ctx, plan)
	if err != nil {
		return model.Plan{}, err
	}
	go svc.runPlan(created)
	return created, nil
}

// runPlan executes a plan's steps in dependency order via delegation, persisting each step's outcome as
// it goes (so a poll of GetPlan shows live progress). Sequential in v1. Runs on a background context.
func (svc *Service) runPlan(plan model.Plan) {
	ctx := context.Background()
	results := map[string]string{} // resolved step key -> answer
	done := map[string]bool{}
	failed := map[string]bool{} // failed OR skipped — either way, "resolved but unusable"
	anyFailed := false

	remaining := len(plan.Steps)
	for remaining > 0 {
		progressed := false
		for i := range plan.Steps {
			s := &plan.Steps[i]
			if done[s.Key] || failed[s.Key] {
				continue
			}
			ready, depFailed := true, false
			for _, d := range s.DependsOn {
				switch {
				case failed[d]:
					depFailed = true
				case !done[d]:
					ready = false
				}
			}
			if !ready {
				continue
			}
			progressed = true
			remaining--

			if depFailed {
				failed[s.Key] = true
				anyFailed = true
				_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepSkipped, "", "skipped: a dependency did not complete")
				continue
			}

			_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepRunning, "", "")
			task := s.Instruction
			if c := planContext(s.DependsOn, results); c != "" {
				task += "\n\n" + c
			}
			res, err := svc.Delegate(ctx, plan.ProjectID, s.Profile, task, profileToolNames(ProfileByID(s.Profile)))
			if err != nil {
				failed[s.Key] = true
				anyFailed = true
				_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepFailed, "", err.Error())
				continue
			}
			done[s.Key] = true
			results[s.Key] = res.Answer
			_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepDone, res.Answer, "")
		}
		if !progressed {
			// A cycle or an unsatisfiable dependency — fail the remainder rather than spin.
			for i := range plan.Steps {
				s := &plan.Steps[i]
				if !done[s.Key] && !failed[s.Key] {
					anyFailed = true
					_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepFailed, "", "unresolved dependency")
				}
			}
			break
		}
	}

	status := model.PlanDone
	if anyFailed {
		status = model.PlanFailed
	}
	_ = svc.store.UpdatePlanStatus(ctx, plan.ID, status)
}

// planContext summarizes completed dependency results as context for a dependent step.
func planContext(deps []string, results map[string]string) string {
	if len(deps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Context from prior steps:")
	for _, d := range deps {
		if r, ok := results[d]; ok {
			fmt.Fprintf(&b, "\n\n[%s]\n%s", d, r)
		}
	}
	return b.String()
}
