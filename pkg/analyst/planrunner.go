package analyst

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// activityEntry is one turn of a sub-agent's live activity trail. It is serialised as a single JSON line
// (JSONL) so the UI can lead with the agent's own commentary and tuck the tool's args/output behind an
// expander, instead of dumping a wall of raw tool lines. The frontend tolerates legacy plain-text lines.
type activityEntry struct {
	Kind    string `json:"k"`              // ok | err | deny | delegate-start | delegate-end
	Tool    string `json:"tool"`           // the tool that ran this turn
	Note    string `json:"note,omitempty"` // the agent's prose for this turn — shown first, most prominent
	Args    string `json:"args,omitempty"` // args preview, revealed on expand
	Out     string `json:"out,omitempty"`  // result/error preview, revealed on expand
	Profile string `json:"profile,omitempty"`
	Depth   int    `json:"depth,omitempty"`
	DurMs   int64  `json:"dur_ms,omitempty"`
	Steps   int    `json:"steps,omitempty"`
}

// clip truncates s to at most max runes, preserving internal newlines (commentary is meant to be read, not
// flattened). Kept distinct from oneLine, which collapses whitespace for one-line previews.
func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > max {
		return string(r[:max]) + "…"
	}
	return s
}

// formatActivity renders one of a sub-agent's tool turns into a JSON line for the step's live activity
// trail: the agent's commentary, the tool, and previews of its args and result/error. One line per turn.
func formatActivity(st agent.Step) string {
	return formatActivityAt(st, 0)
}

func formatActivityAt(st agent.Step, depth int) string {
	e := activityEntry{Kind: "ok", Tool: st.Call.Tool, Note: clip(st.Note, 800), Depth: depth}
	if len(st.Call.Args) > 0 {
		if b, err := json.Marshal(st.Call.Args); err == nil {
			e.Args = oneLine(string(b), 400)
		}
	}
	switch {
	case st.Error != "":
		e.Kind, e.Out = "err", clip(st.Error, 600)
	case st.Result == "(denied)":
		e.Kind = "deny"
	default:
		e.Out = clip(st.Result, 600)
	}
	b, err := json.Marshal(e)
	if err != nil {
		return fmt.Sprintf("→ %s — %s\n", st.Call.Tool, oneLine(st.Result, 160))
	}
	return string(b) + "\n"
}

func formatDelegateStart(profile, task string, depth int) string {
	e := activityEntry{Kind: "delegate-start", Tool: "delegate", Profile: profile, Note: clip(task, 200), Depth: depth}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

func formatDelegateEnd(profile string, depth int, res DelegationResult, dur time.Duration) string {
	e := activityEntry{Kind: "delegate-end", Tool: "delegate", Profile: profile, Depth: depth,
		DurMs: dur.Milliseconds(), Steps: res.StepCount, Note: clip(res.Answer, 200)}
	b, _ := json.Marshal(e)
	return string(b) + "\n"
}

// activityAppender carries a raw line-appender through the context so both the plan runner's progress
// sink and the delegation trace can write to the same step progress field. The depth tracks the current
// delegation nesting level within that step.
type activityAppender struct {
	mu     sync.Mutex
	write  func(line string)
	depth  int
	closed bool
}

type activityAppenderKey struct{}

func withActivityAppender(ctx context.Context, a *activityAppender) context.Context {
	return context.WithValue(ctx, activityAppenderKey{}, a)
}

func getActivityAppender(ctx context.Context) *activityAppender {
	a, _ := ctx.Value(activityAppenderKey{}).(*activityAppender)
	return a
}

func (a *activityAppender) append(line string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.closed {
		a.write(line)
	}
}

func (a *activityAppender) record(st agent.Step) {
	a.append(formatActivityAt(st, a.depth))
}

func (a *activityAppender) nested(profile string, depth int) *activityAppender {
	return &activityAppender{write: a.write, depth: depth}
}

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

// defaultMaxParallelSteps is the process-wide default for how many plan steps run concurrently. A
// playbook's MaxConcurrency overrides it; OSB_PLAN_MAX_PARALLEL overrides both.
var defaultMaxParallelSteps = envInt("OSB_PLAN_MAX_PARALLEL", 4)

// concurrencyCap returns the effective concurrency limit for a plan run: the playbook's MaxConcurrency
// if set, else the global default. OSB_PLAN_MAX_PARALLEL overrides everything.
func (svc *Service) concurrencyCap(ctx context.Context, plan model.Plan) int {
	if v := os.Getenv("OSB_PLAN_MAX_PARALLEL"); v != "" {
		return envInt("OSB_PLAN_MAX_PARALLEL", defaultMaxParallelSteps)
	}
	if pb, err := svc.resolvePlaybook(ctx, plan.PlaybookID); err == nil && pb.MaxConcurrency > 0 {
		return pb.MaxConcurrency
	}
	return defaultMaxParallelSteps
}

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

	expandedSteps := svc.expandSteps(ctx, projectID, pb.Steps)

	plan := model.Plan{ProjectID: projectID, PlaybookID: pb.ID, Goal: pb.Goal, Status: model.PlanRunning}
	for _, s := range expandedSteps {
		plan.Steps = append(plan.Steps, model.PlanStep{
			Key: s.Key, Profile: s.Profile, Instruction: s.Instruction,
			DependsOn: s.DependsOn, Gate: s.Gate, SkipIf: s.SkipIf,
		})
	}
	created, err := svc.p(projectID).CreatePlan(ctx, plan)
	if err != nil {
		return model.Plan{}, err
	}
	svc.launchPlan(created)
	return created, nil
}

// runPlan executes a plan's steps in dependency order via delegation, persisting each step's outcome as
// it goes (so a poll of GetPlan shows live progress).
//
// It uses a PIPELINED scheduler: each step starts the moment its own dependencies finish, without waiting
// for an entire wave of peers to complete. This reduces wall-clock time from "sum of slowest per wave" to
// "critical path through the DAG." Concurrency is bounded by the playbook's MaxConcurrency (or the global
// default). It is resume-safe (ADR-0044): it reloads the plan and reconstructs progress from persisted step
// statuses, so it can be relaunched after a mid-run approval pause and pick up exactly where it left off.
//
// When a step fails, in-flight steps whose output has no remaining live consumer are cancelled — so tokens
// aren't wasted on work whose results will go unused. Conditional steps (SkipIf) are evaluated before
// delegation; a step whose condition is unmet is skipped with a reason, and its dependents run normally
// (a skipped-by-condition step is treated as done, not failed, so dependents proceed).
func (svc *Service) runPlan(ctx context.Context, plan model.Plan) {
	defer unregisterPlan(plan.ID)

	if reloaded, err := svc.p(plan.ProjectID).GetPlan(ctx, plan.ID); err == nil {
		plan = reloaded
	}

	var mu sync.Mutex
	results := map[string]string{}
	done := map[string]bool{}
	failed := map[string]bool{}
	anyFailed := false
	inFlight := map[string]context.CancelFunc{}

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

	completions := make(chan string, len(plan.Steps))
	cap := svc.concurrencyCap(ctx, plan)
	sem := make(chan struct{}, cap)

	stepByKey := map[string]*model.PlanStep{}
	for i := range plan.Steps {
		stepByKey[plan.Steps[i].Key] = &plan.Steps[i]
	}

	dependents := map[string][]string{}
	for _, s := range plan.Steps {
		for _, d := range s.DependsOn {
			dependents[d] = append(dependents[d], s.Key)
		}
	}

	// startStep launches a step goroutine. MUST be called with mu held — it reads results and writes
	// inFlight under the caller's lock, then spawns a goroutine that acquires mu independently.
	startStep := func(s *model.PlanStep) {
		task := s.Instruction
		if c := planContext(s.DependsOn, results); c != "" {
			task += "\n\n" + c
		}
		stepCtx, cancel := context.WithCancel(ctx)
		inFlight[s.Key] = cancel
		stepKey := s.Key
		stepID := s.ID
		stepProfile := s.Profile

		go func() {
			select {
			case sem <- struct{}{}:
			case <-stepCtx.Done():
				mu.Lock()
				if !done[stepKey] && !failed[stepKey] {
					failed[stepKey] = true
					anyFailed = true
					svc.setStep(ctx, plan.ProjectID, stepID, model.StepSkipped, "", "cancelled: output no longer needed after a sibling failed")
				}
				delete(inFlight, stepKey)
				mu.Unlock()
				completions <- stepKey
				return
			}
			defer func() { <-sem }()
			defer func() {
				mu.Lock()
				delete(inFlight, stepKey)
				mu.Unlock()
				completions <- stepKey
			}()

			svc.setStep(ctx, plan.ProjectID, stepID, model.StepRunning, "", "")

			appender := &activityAppender{
				write: func(line string) {
					_ = svc.p(plan.ProjectID).AppendPlanStepProgress(context.Background(), stepID, line)
				},
			}
			stepCtx = withActivityAppender(stepCtx, appender)
			stepCtx = withProgressSink(stepCtx, appender.record)

			res, err := svc.Delegate(stepCtx, plan.ProjectID, stepProfile, task, profileToolNames(svc.resolveProfile(ctx, stepProfile)))

			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if stepCtx.Err() != nil && ctx.Err() == nil {
					failed[stepKey] = true
					anyFailed = true
					svc.setStep(ctx, plan.ProjectID, stepID, model.StepSkipped, "", "cancelled: output no longer needed after a sibling failed")
				} else {
					failed[stepKey] = true
					anyFailed = true
					svc.setStep(ctx, plan.ProjectID, stepID, model.StepFailed, "", err.Error())
				}
				return
			}
			if res.Stopped {
				failed[stepKey] = true
				anyFailed = true
				svc.setStep(ctx, plan.ProjectID, stepID, model.StepFailed, "", "sub-agent stopped without a final answer (reached its step limit); no result produced")
				return
			}
			done[stepKey] = true
			results[stepKey] = res.Answer
			svc.setStep(ctx, plan.ProjectID, stepID, model.StepDone, res.Answer, "")
		}()
	}

	// cancelUseless cancels in-flight steps whose output has no remaining live consumer: every step
	// that depends on them is already done, failed, or skipped. Terminal steps (nothing depends on
	// them) are always kept — their result is the plan's output.
	cancelUseless := func() {
		for key, cancel := range inFlight {
			deps := dependents[key]
			if len(deps) == 0 {
				continue
			}
			allDead := true
			for _, d := range deps {
				if !failed[d] && !done[d] {
					allDead = false
					break
				}
			}
			if allDead {
				cancel()
			}
		}
	}

	// evaluate propagates failures, resolves cleared gates, and starts newly ready steps. Returns
	// true if the plan parks at an uncleared gate (the caller must drain in-flight before returning).
	evaluate := func() (parked bool) {
		mu.Lock()
		defer mu.Unlock()

		// Phase 1: propagate failures and resolve cleared gates (loop until stable).
		changed := true
		for changed {
			changed = false
			for i := range plan.Steps {
				s := &plan.Steps[i]
				if done[s.Key] || failed[s.Key] || inFlight[s.Key] != nil {
					continue
				}
				isReady, depFailed := stepReady(s, done, failed)
				if depFailed {
					failed[s.Key] = true
					anyFailed = true
					svc.setStep(ctx, plan.ProjectID, s.ID, model.StepSkipped, "", "skipped: a dependency did not complete")
					changed = true
					continue
				}
				if isReady && s.Gate && s.GateApproved {
					done[s.Key] = true
					results[s.Key] = s.Result
					svc.setStep(ctx, plan.ProjectID, s.ID, model.StepDone, s.Result, "")
					changed = true
				}
			}
		}

		// After failure propagation, cancel in-flight steps that are now useless.
		cancelUseless()

		// Phase 2: check for uncleared gates among ready steps.
		for i := range plan.Steps {
			s := &plan.Steps[i]
			if done[s.Key] || failed[s.Key] || inFlight[s.Key] != nil {
				continue
			}
			isReady, _ := stepReady(s, done, failed)
			if isReady && s.Gate {
				svc.setStep(ctx, plan.ProjectID, s.ID, model.StepWaiting, "", "awaiting human approval")
				svc.setPlanStatus(ctx, plan.ProjectID, plan.ID, model.PlanWaiting)
				svc.notifyPlanWaiting(ctx, plan, *s)
				return true
			}
		}

		// Phase 3: start ready non-gate steps, evaluating conditions first.
		for i := range plan.Steps {
			s := &plan.Steps[i]
			if done[s.Key] || failed[s.Key] || inFlight[s.Key] != nil {
				continue
			}
			isReady, _ := stepReady(s, done, failed)
			if !isReady {
				continue
			}
			if skip, reason := svc.evaluateCondition(ctx, plan.ProjectID, s.SkipIf); skip {
				done[s.Key] = true
				results[s.Key] = "skipped: " + reason
				svc.setStep(ctx, plan.ProjectID, s.ID, model.StepDone, results[s.Key], "")
				continue
			}
			startStep(s)
		}
		return false
	}

	// Initial evaluation: start all initially-ready steps.
	if evaluate() {
		drainInFlight(ctx, &mu, inFlight, completions)
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		mu.Lock()
		allDone := allResolved(plan.Steps, done, failed)
		nInFlight := len(inFlight)
		mu.Unlock()

		if allDone {
			break
		}
		if nInFlight == 0 {
			mu.Lock()
			for i := range plan.Steps {
				s := &plan.Steps[i]
				if !done[s.Key] && !failed[s.Key] {
					anyFailed = true
					svc.setStep(ctx, plan.ProjectID, s.ID, model.StepFailed, "", "unresolved dependency")
				}
			}
			mu.Unlock()
			break
		}

		select {
		case <-completions:
		case <-ctx.Done():
			return
		}

		if evaluate() {
			drainInFlight(ctx, &mu, inFlight, completions)
			return
		}
	}

	status := model.PlanDone
	if anyFailed {
		status = model.PlanFailed
	}
	svc.setPlanStatus(ctx, plan.ProjectID, plan.ID, status)
}

// drainInFlight waits for all in-flight steps to finish (so their results are persisted before the plan
// parks). Called when a gate is about to park the plan.
func drainInFlight(ctx context.Context, mu *sync.Mutex, inFlight map[string]context.CancelFunc, completions <-chan string) {
	for {
		mu.Lock()
		n := len(inFlight)
		mu.Unlock()
		if n == 0 {
			return
		}
		select {
		case <-completions:
		case <-ctx.Done():
			return
		}
	}
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

// --- Per-asset step expansion ---

// expandSteps expands playbook steps with PerAsset set into one copy per matching project asset, and
// rewrites downstream DependsOn references so dependents wait on all expanded copies. Steps without
// PerAsset pass through unchanged. If PerAsset is set but no assets of that type exist, the step is
// omitted (there is nothing to scan).
func (svc *Service) expandSteps(ctx context.Context, projectID string, steps []PlaybookStep) []PlaybookStep {
	assets, _ := svc.p(projectID).ListAssets(ctx)
	if len(assets) == 0 {
		return steps
	}

	expansions := map[string][]string{}
	var out []PlaybookStep

	for _, s := range steps {
		if s.PerAsset == "" {
			out = append(out, s)
			continue
		}
		var matching []model.Asset
		for _, a := range assets {
			if a.Type == s.PerAsset {
				matching = append(matching, a)
			}
		}
		if len(matching) == 0 {
			continue
		}
		if len(matching) == 1 {
			expanded := s
			expanded.PerAsset = ""
			expanded.Instruction = "Target asset: " + assetLabel(matching[0]) + " (id " + matching[0].ID + ")\n\n" + s.Instruction
			out = append(out, expanded)
			expansions[s.Key] = []string{s.Key}
			continue
		}
		var keys []string
		for _, a := range matching {
			key := s.Key + "@" + assetShortName(a)
			keys = append(keys, key)
			out = append(out, PlaybookStep{
				Key:         key,
				Profile:     s.Profile,
				Instruction: "Target asset: " + assetLabel(a) + " (id " + a.ID + ")\n\n" + s.Instruction,
				DependsOn:   s.DependsOn,
				Gate:        s.Gate,
				SkipIf:      s.SkipIf,
			})
		}
		expansions[s.Key] = keys
	}

	if len(expansions) > 0 {
		for i := range out {
			var newDeps []string
			for _, d := range out[i].DependsOn {
				if expanded, ok := expansions[d]; ok {
					newDeps = append(newDeps, expanded...)
				} else {
					newDeps = append(newDeps, d)
				}
			}
			out[i].DependsOn = newDeps
		}
	}
	return out
}

func assetShortName(a model.Asset) string {
	loc := a.Location
	if i := strings.LastIndex(loc, "/"); i >= 0 && i < len(loc)-1 {
		loc = loc[i+1:]
	}
	if loc == "" {
		if len(a.ID) > 8 {
			return a.ID[:8]
		}
		return a.ID
	}
	return loc
}

func assetLabel(a model.Asset) string {
	if a.Location != "" {
		return a.Location
	}
	return a.ID
}

// --- Conditional step evaluation ---

// evaluateCondition checks whether a step's SkipIf condition is met. Returns (should_skip, reason).
// Conditions check project state (assets, ecosystems) so the plan runner can skip a step programmatically
// before burning tokens on a sub-agent that would just discover the same thing.
func (svc *Service) evaluateCondition(ctx context.Context, projectID, cond string) (bool, string) {
	if cond == "" {
		return false, ""
	}

	switch {
	case cond == "no_go_modules":
		return svc.condNoGoModules(ctx, projectID)
	case strings.HasPrefix(cond, "no_ecosystem:"):
		eco := strings.TrimPrefix(cond, "no_ecosystem:")
		return svc.condNoEcosystem(ctx, projectID, eco)
	case strings.HasPrefix(cond, "no_assets:"):
		assetType := strings.TrimPrefix(cond, "no_assets:")
		return svc.condNoAssets(ctx, projectID, assetType)
	default:
		return false, ""
	}
}

func (svc *Service) condNoGoModules(ctx context.Context, projectID string) (bool, string) {
	assets, _ := svc.p(projectID).ListAssets(ctx)
	for _, a := range assets {
		if a.Type != model.AssetSourceRepo {
			continue
		}
		for _, eco := range a.Ecosystems {
			if eco == "go" {
				return false, ""
			}
		}
		if _, err := os.Stat(filepath.Join(a.Location, "go.mod")); err == nil {
			return false, ""
		}
	}
	return true, "no Go modules found in any source asset"
}

func (svc *Service) condNoEcosystem(ctx context.Context, projectID, eco string) (bool, string) {
	assets, _ := svc.p(projectID).ListAssets(ctx)
	for _, a := range assets {
		if a.Type != model.AssetSourceRepo {
			continue
		}
		for _, e := range a.Ecosystems {
			if e == eco {
				return false, ""
			}
		}
	}
	return true, "no " + eco + " assets found"
}

func (svc *Service) condNoAssets(ctx context.Context, projectID, assetType string) (bool, string) {
	assets, _ := svc.p(projectID).ListAssets(ctx)
	for _, a := range assets {
		if a.Type == assetType {
			return false, ""
		}
	}
	return true, "no " + assetType + " assets in this project"
}
