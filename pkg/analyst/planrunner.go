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
		plan.Steps = append(plan.Steps, model.PlanStep{Key: s.Key, Profile: s.Profile, Instruction: s.Instruction, DependsOn: s.DependsOn, Gate: s.Gate})
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
//
// It is resume-safe (ADR-0044): it reloads the plan and reconstructs progress from persisted step statuses,
// so it can be relaunched after a mid-run approval pause and pick up exactly where it left off. When it
// reaches a not-yet-approved gate step whose dependencies are complete, it parks the plan in 'waiting' and
// returns; a human's ResolvePlanGate clears (or denies) the gate and relaunches the run.
func (svc *Service) runPlan(plan model.Plan) {
	ctx := context.Background()
	// Reload so a resumed run sees the persisted step ids/statuses/results (and any just-resolved gate).
	if reloaded, err := svc.store.GetPlan(ctx, plan.ID); err == nil {
		plan = reloaded
	}

	results := map[string]string{} // resolved step key -> answer
	done := map[string]bool{}
	failed := map[string]bool{} // failed OR skipped — either way, "resolved but unusable"
	anyFailed := false
	for _, s := range plan.Steps {
		switch s.Status {
		case model.StepDone:
			done[s.Key] = true
			results[s.Key] = s.Result
		case model.StepFailed, model.StepSkipped:
			failed[s.Key] = true
			anyFailed = true
		}
	}

	for {
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

			if depFailed {
				failed[s.Key] = true
				anyFailed = true
				progressed = true
				_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepSkipped, "", "skipped: a dependency did not complete")
				continue
			}

			// A gate is a human-approval checkpoint, not a delegated task. Uncleared, it parks the plan:
			// record the pause and stop the run (a later ResolvePlanGate approves or denies it, then
			// relaunches runPlan). Once approved, it resolves as done immediately, carrying its note forward.
			if s.Gate {
				if !s.GateApproved {
					_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepWaiting, "", "awaiting human approval")
					_ = svc.store.UpdatePlanStatus(ctx, plan.ID, model.PlanWaiting)
					svc.notifyPlanWaiting(ctx, plan, *s)
					return
				}
				done[s.Key] = true
				results[s.Key] = s.Result
				progressed = true
				_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepDone, s.Result, "")
				continue
			}

			progressed = true
			_ = svc.store.UpdatePlanStep(ctx, s.ID, model.StepRunning, "", "")
			task := s.Instruction
			if c := planContext(s.DependsOn, results); c != "" {
				task += "\n\n" + c
			}
			res, err := svc.Delegate(ctx, plan.ProjectID, s.Profile, task, profileToolNames(svc.resolveProfile(ctx, s.Profile)))
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
		if allResolved(plan.Steps, done, failed) {
			break
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

func allResolved(steps []model.PlanStep, done, failed map[string]bool) bool {
	for _, s := range steps {
		if !done[s.Key] && !failed[s.Key] {
			return false
		}
	}
	return true
}

// ResolvePlanGate records a human's decision on a plan's waiting gate and resumes the run (ADR-0044).
// Approving clears the gate so the step executes; denying skips it (its dependents are skipped and the plan
// ends). The plan must be waiting on the given gate step, else it returns an error.
func (svc *Service) ResolvePlanGate(ctx context.Context, planID, stepID string, approve bool, note string) (model.Plan, error) {
	plan, err := svc.store.GetPlan(ctx, planID)
	if err != nil {
		return model.Plan{}, err
	}
	if plan.Status != model.PlanWaiting {
		return model.Plan{}, fmt.Errorf("plan %s is not awaiting approval (status %s)", planID, plan.Status)
	}
	var gate *model.PlanStep
	for i := range plan.Steps {
		if plan.Steps[i].ID == stepID {
			gate = &plan.Steps[i]
			break
		}
	}
	if gate == nil || !gate.Gate || gate.Status != model.StepWaiting {
		return model.Plan{}, errors.New("no waiting gate step with that id on this plan")
	}
	if err := svc.store.ResolvePlanGate(ctx, stepID, approve, note); err != nil {
		return model.Plan{}, err
	}
	// Flip back to running and relaunch; the resumed run reconstructs state and continues (or ends).
	_ = svc.store.UpdatePlanStatus(ctx, planID, model.PlanRunning)
	go svc.runPlan(plan)
	return svc.store.GetPlan(ctx, planID)
}

// notifyPlanWaiting raises a notification so a human knows a plan has paused for their approval.
func (svc *Service) notifyPlanWaiting(ctx context.Context, plan model.Plan, gate model.PlanStep) {
	pid := plan.ProjectID
	_, _ = svc.store.CreateNotification(ctx, model.Notification{
		Kind:      model.NotifyApproval,
		Title:     "Assessment paused for approval",
		Body:      "Step \"" + gate.Key + "\" needs your approval before the run continues.",
		ProjectID: &pid,
		Link:      "plan:" + plan.ID,
	})
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
