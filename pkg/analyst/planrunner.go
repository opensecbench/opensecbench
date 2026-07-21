package analyst

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// planCancels tracks in-flight plan runs so a human can stop them. Plans run in background goroutines that
// outlive any single request, and the analyst Service is created per-request, so this registry is
// process-level (one per process). Cancelling a plan's context aborts its in-flight LLM call (HTTP request
// or `claude` subprocess) and stops the runner from starting further steps.
var planCancels = struct {
	mu sync.Mutex
	m  map[string]context.CancelFunc
}{m: map[string]context.CancelFunc{}}

func registerPlan(id string, cancel context.CancelFunc) {
	planCancels.mu.Lock()
	planCancels.m[id] = cancel
	planCancels.mu.Unlock()
}

func unregisterPlan(id string) {
	planCancels.mu.Lock()
	delete(planCancels.m, id)
	planCancels.mu.Unlock()
}

// cancelPlanRun aborts a running plan's context, returning whether one was in flight.
func cancelPlanRun(id string) bool {
	planCancels.mu.Lock()
	defer planCancels.mu.Unlock()
	if cancel, ok := planCancels.m[id]; ok {
		cancel()
		delete(planCancels.m, id)
		return true
	}
	return false
}

// launchPlan starts (or relaunches) a plan run under a cancellable context registered for stopping.
func (svc *Service) launchPlan(plan model.Plan) {
	ctx, cancel := context.WithCancel(context.Background())
	registerPlan(plan.ID, cancel)
	go svc.runPlan(ctx, plan)
}

// CancelPlan stops a running (or approval-waiting) plan: it aborts the in-flight LLM call, marks the plan
// cancelled, and skips any steps that hadn't finished — so nothing keeps burning tokens.
func (svc *Service) CancelPlan(ctx context.Context, projectID, planID string) error {
	plan, err := svc.p(projectID).GetPlan(ctx, planID)
	if err != nil {
		return err
	}
	if plan.Status != model.PlanRunning && plan.Status != model.PlanWaiting {
		return fmt.Errorf("plan is %s, not running", plan.Status)
	}
	cancelPlanRun(planID) // abort in-flight work
	for _, s := range plan.Steps {
		switch s.Status {
		case model.StepPending, model.StepRunning, model.StepWaiting:
			svc.setStep(ctx, projectID, s.ID, model.StepSkipped, "", "cancelled")
		}
	}
	svc.setPlanStatus(ctx, projectID, planID, model.PlanCancelled)
	return nil
}

// setStep / setPlanStatus persist plan progress, logging on failure instead of silently dropping it — a
// dropped write here means an inspecting UI (or a resumed run) sees stale state, so failures must be visible.
func (svc *Service) setStep(ctx context.Context, projectID, stepID, status, result, note string) {
	if err := svc.p(projectID).UpdatePlanStep(ctx, stepID, status, result, note); err != nil {
		log.Printf("planrunner: persisting step %s=%s failed: %v", stepID, status, err)
	}
}
func (svc *Service) setPlanStatus(ctx context.Context, projectID, planID, status string) {
	if err := svc.p(projectID).UpdatePlanStatus(ctx, planID, status); err != nil {
		log.Printf("planrunner: persisting plan %s=%s failed: %v", planID, status, err)
	}
}

// maxParallelSteps bounds how many plan steps run concurrently in one wave (ADR-0046). Steps often run
// Docker-heavy capabilities, so this is deliberately modest; a plan with a wider ready set just takes more
// waves. Independent steps (e.g. the SAST/SCA/secrets scanners) still overlap instead of running serially.
const maxParallelSteps = 4

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
	created, err := svc.p(projectID).CreatePlan(ctx, plan)
	if err != nil {
		return model.Plan{}, err
	}
	svc.launchPlan(created)
	return created, nil
}

// runPlan executes a plan's steps in dependency order via delegation, persisting each step's outcome as
// it goes (so a poll of GetPlan shows live progress). Runs on a background context.
//
// It schedules in waves: each pass runs ALL currently-ready steps concurrently (bounded by
// maxParallelSteps), so independent steps overlap instead of running one at a time (ADR-0046). It is
// resume-safe (ADR-0044): it reloads the plan and reconstructs progress from persisted step statuses, so it
// can be relaunched after a mid-run approval pause and pick up exactly where it left off. When a
// not-yet-approved gate step's dependencies are complete, it parks the plan in 'waiting' and returns; a
// human's ResolvePlanGate clears (or denies) the gate and relaunches the run.
func (svc *Service) runPlan(ctx context.Context, plan model.Plan) {
	defer unregisterPlan(plan.ID)
	// Reload so a resumed run sees the persisted step ids/statuses/results (and any just-resolved gate).
	if reloaded, err := svc.p(plan.ProjectID).GetPlan(ctx, plan.ID); err == nil {
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
		// Stopped by a human: abort before starting any more work. CancelPlan has persisted the terminal
		// state (plan cancelled, unfinished steps skipped), so just bow out.
		if ctx.Err() != nil {
			return
		}
		progressed := false

		// Collect the ready set (dependencies satisfied), skipping steps whose dependency failed.
		var ready []*model.PlanStep
		for i := range plan.Steps {
			s := &plan.Steps[i]
			if done[s.Key] || failed[s.Key] {
				continue
			}
			isReady, depFailed := stepReady(s, done, failed)
			if depFailed {
				failed[s.Key] = true
				anyFailed = true
				progressed = true
				svc.setStep(ctx, plan.ProjectID, s.ID, model.StepSkipped, "", "skipped: a dependency did not complete")
				continue
			}
			if isReady {
				ready = append(ready, s)
			}
		}

		// Approved gates are checkpoints, not tasks — resolve them as done (carrying the note forward) and
		// re-evaluate, so their dependents become ready without consuming a delegation wave.
		clearedGate := false
		for _, s := range ready {
			if s.Gate && s.GateApproved {
				done[s.Key] = true
				results[s.Key] = s.Result
				clearedGate = true
				svc.setStep(ctx, plan.ProjectID, s.ID, model.StepDone, s.Result, "")
			}
		}
		if clearedGate {
			continue
		}

		// An uncleared gate parks the whole plan: record the pause and stop (a later ResolvePlanGate approves
		// or denies it, then relaunches runPlan). We pause before starting new work so nothing runs that a
		// human might veto.
		for _, s := range ready {
			if s.Gate {
				svc.setStep(ctx, plan.ProjectID, s.ID, model.StepWaiting, "", "awaiting human approval")
				svc.setPlanStatus(ctx, plan.ProjectID, plan.ID, model.PlanWaiting)
				svc.notifyPlanWaiting(ctx, plan, *s)
				return
			}
		}

		// Run all ready (non-gate) steps concurrently as one wave.
		var wave []*model.PlanStep
		for _, s := range ready {
			wave = append(wave, s)
		}
		if len(wave) > 0 {
			svc.runWave(ctx, plan.ProjectID, wave, results, done, failed, &anyFailed)
			progressed = true
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
					svc.setStep(ctx, plan.ProjectID, s.ID, model.StepFailed, "", "unresolved dependency")
				}
			}
			break
		}
	}

	status := model.PlanDone
	if anyFailed {
		status = model.PlanFailed
	}
	svc.setPlanStatus(ctx, plan.ProjectID, plan.ID, status)
}

// stepReady reports whether a step's dependencies are all satisfied (isReady), or whether any dependency
// failed/was skipped (depFailed — the step can never run and should be skipped).
func stepReady(s *model.PlanStep, done, failed map[string]bool) (isReady, depFailed bool) {
	isReady = true
	for _, d := range s.DependsOn {
		switch {
		case failed[d]:
			depFailed = true
		case !done[d]:
			isReady = false
		}
	}
	return isReady, depFailed
}

// runWave delegates a set of independent, ready steps concurrently (bounded by maxParallelSteps) and blocks
// until all finish, folding their outcomes back into the shared done/failed/results maps under a mutex. Each
// step's dependency context is read (also under the mutex) before its delegation — dependencies are from
// earlier waves, so their results are stable while this wave runs.
func (svc *Service) runWave(ctx context.Context, projectID string, wave []*model.PlanStep, results map[string]string, done, failed map[string]bool, anyFailed *bool) {
	sem := make(chan struct{}, maxParallelSteps)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, s := range wave {
		s := s
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			svc.setStep(ctx, projectID, s.ID, model.StepRunning, "", "")
			mu.Lock()
			task := s.Instruction
			if c := planContext(s.DependsOn, results); c != "" {
				task += "\n\n" + c
			}
			mu.Unlock()

			res, err := svc.Delegate(ctx, projectID, s.Profile, task, profileToolNames(svc.resolveProfile(ctx, s.Profile)))

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed[s.Key] = true
				*anyFailed = true
				svc.setStep(ctx, projectID, s.ID, model.StepFailed, "", err.Error())
				return
			}
			// A sub-agent that hit its step cap without a final answer produced no real work product — mark
			// the step failed (not done) so the run reads honestly and dependents don't build on nothing.
			if res.Stopped {
				failed[s.Key] = true
				*anyFailed = true
				svc.setStep(ctx, projectID, s.ID, model.StepFailed, "", "sub-agent stopped without a final answer (reached its step limit); no result produced")
				return
			}
			done[s.Key] = true
			results[s.Key] = res.Answer
			svc.setStep(ctx, projectID, s.ID, model.StepDone, res.Answer, "")
		}()
	}
	wg.Wait()
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
func (svc *Service) ResolvePlanGate(ctx context.Context, projectID, planID, stepID string, approve bool, note string) (model.Plan, error) {
	plan, err := svc.p(projectID).GetPlan(ctx, planID)
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
	if err := svc.p(projectID).ResolvePlanGate(ctx, stepID, approve, note); err != nil {
		return model.Plan{}, err
	}
	// Flip back to running and relaunch; the resumed run reconstructs state and continues (or ends).
	svc.setPlanStatus(ctx, projectID, planID, model.PlanRunning)
	svc.launchPlan(plan)
	return svc.p(projectID).GetPlan(ctx, planID)
}

// notifyPlanWaiting raises a notification so a human knows a plan has paused for their approval.
func (svc *Service) notifyPlanWaiting(ctx context.Context, plan model.Plan, gate model.PlanStep) {
	pid := plan.ProjectID
	_, _ = svc.p(plan.ProjectID).CreateNotification(ctx, model.Notification{
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
