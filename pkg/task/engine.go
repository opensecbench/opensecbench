// Package task runs capabilities and records their provenance. The engine ties together the
// capability registry, a sandboxed runner (ADR-0004), the content-addressed store, and the
// database so that every run produces a task and immutable output artifacts with full lineage.
package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/enrich"
	"github.com/opensecbench/opensecbench/pkg/interpret"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/scope"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// ErrTaskNotRunning is returned when cancelling a task that is not currently executing.
var ErrTaskNotRunning = errors.New("task: not running")

// Engine executes capabilities and persists their provenance. Runs are asynchronous and the queue is
// durable (ADR-0023): the tasks table IS the queue. Enqueue records a pending task; a bounded worker
// pool atomically claims pending rows, so pending work survives a control-plane restart and interrupted
// runs resume — callers never block on a container.
type Engine struct {
	mgr      *store.Manager
	casr     cas.Resolver
	registry *capability.Registry
	runner   runner.Runner

	// Secrets, if set, resolves a vault secret name to its plaintext for exec-time injection
	// (ADR-0011). nil disables secret injection.
	Secrets func(ctx context.Context, name string) (string, error)

	// resolveRunner, if set, returns the runner for a task's RunnerTarget (a remote runner id). When a
	// task targets a remote runner and this is nil or errors, the task fails cleanly (ADR-0024).
	resolveRunner func(runnerID string) (runner.Runner, error)

	// outdatedHTTP, if set, is the HTTP client used to query deps.dev for the outdated-dependency
	// enrichment after a syft SBOM completes. nil disables it (tests / offline).
	outdatedHTTP enrich.Doer

	// onComplete, if set, is called after every task's execution finishes (success or failure) with its
	// final Outcome. It's the seam the methodology runner hooks (ADR-0056) to route a task's observations
	// back to the item that spawned it and flip coverage. nil in most contexts.
	onComplete func(ctx context.Context, oc Outcome)

	notify     chan struct{}   // "there may be work" wakeup, so workers claim immediately on enqueue
	baseCtx    context.Context // parent context for background runs (survives request cancellation)
	baseCancel context.CancelFunc
	wg         sync.WaitGroup // worker goroutines

	mu      sync.Mutex
	running map[string]runState

	// reEvalMu serializes retroactive re-evaluation so two capabilities finishing at once can't both
	// promote the same observation into a duplicate finding.
	reEvalMu sync.Mutex
}

type runState struct {
	cancel    context.CancelFunc
	container string
}

// g is the instance-wide database (audit). p is the per-project database for a task's project; it falls
// back to global if the project can't be resolved so a nil handle never panics (ADR-0049). pidOf/pidPtr
// unwrap the optional project id that tasks and requests carry.
func (e *Engine) g() *store.DB { return e.mgr.Global() }

// casFor returns the content store owning a task's project blobs (ADR-0049), nil if unresolved.
func (e *Engine) casFor(projectID string) *cas.Store {
	if e.casr == nil {
		return nil
	}
	st, err := e.casr.For(projectID)
	if err != nil {
		return nil
	}
	return st
}

func (e *Engine) p(projectID string) *store.DB {
	if e.mgr == nil {
		return nil
	}
	db, err := e.mgr.Project(projectID)
	if err != nil || db == nil {
		return e.mgr.Global()
	}
	return db
}

func pidOf(t model.Task) string {
	if t.ProjectID != nil {
		return *t.ProjectID
	}
	return ""
}
func pidPtr(p *string) string {
	if p != nil {
		return *p
	}
	return ""
}

// pollInterval is the worker backstop poll — the belt to notify's braces (catches restart-resumed work
// and any missed wakeup). Kept short since a local claim is cheap.
const pollInterval = time.Second

// maxAttempts caps how many times an interrupted task is re-run before it is failed, so a task that
// keeps crashing the process can't loop forever. OSB_TASK_MAX_ATTEMPTS overrides.
func maxAttempts() int {
	n := 3
	if v := os.Getenv("OSB_TASK_MAX_ATTEMPTS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	return n
}

// workerCount bounds concurrent capability runs; OSB_TASK_WORKERS overrides. A burst of scheduled or
// triggered runs queues up rather than spawning unbounded containers.
func workerCount() int {
	n := 3
	if v := os.Getenv("OSB_TASK_WORKERS"); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			n = p
		}
	}
	return n
}

// NewEngine wires the engine's dependencies and starts the async worker pool. Tasks left running by a
// prior process (a crash mid-run) are requeued to pending so the pool resumes them (ADR-0023).
func NewEngine(mgr *store.Manager, casr cas.Resolver, reg *capability.Registry, r runner.Runner) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	workers := workerCount()
	e := &Engine{
		mgr: mgr, casr: casr, registry: reg, runner: r,
		notify:     make(chan struct{}, workers),
		baseCtx:    ctx,
		baseCancel: cancel,
		running:    make(map[string]runState),
	}
	if mgr != nil {
		if n, err := mgr.RequeueInterruptedTasks(ctx); err == nil && n > 0 {
			log.Printf("task: requeued %d interrupted task(s) on startup", n)
		}
		// Plans/playbook runs aren't durably re-drivable like tasks — their coordinating goroutine dies with
		// the process — so a run left "running" by a restart is a ghost. Fail them so they don't show as
		// running forever (a "waiting" plan is left alone; it's parked on a gate and resumes on approval).
		if n, err := mgr.FailUnfinishedPlans(ctx); err == nil && n > 0 {
			log.Printf("plan: failed %d interrupted plan(s) on startup", n)
		}
		if n, err := mgr.FailUnfinishedPlaybookRuns(ctx); err == nil && n > 0 {
			log.Printf("playbook: failed %d interrupted run(s) on startup", n)
		}
	}
	for i := 0; i < workers; i++ {
		e.wg.Add(1)
		go e.worker()
	}
	e.signal() // pick up any pre-existing / just-requeued pending work
	return e
}

// Close stops the worker pool and cancels any in-flight runs. Safe to call once.
func (e *Engine) Close() {
	e.baseCancel()
	e.wg.Wait()
}

// signal is a non-blocking nudge that pending work may be available.
func (e *Engine) signal() {
	select {
	case e.notify <- struct{}{}:
	default:
	}
}

// worker claims and runs pending tasks. It loops claiming as long as there is work (waking a sibling
// each time in case there is more), then parks on notify or the backstop poll until the next wakeup.
func (e *Engine) worker() {
	defer e.wg.Done()
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		task, ok, err := e.mgr.ClaimNextPendingTask(e.baseCtx)
		if err != nil {
			if e.baseCtx.Err() != nil {
				return
			}
			log.Printf("task: claim failed: %v", err)
		} else if ok {
			e.signal() // more may be pending — wake a sibling
			e.runClaimed(task)
			continue
		}
		select {
		case <-e.notify:
		case <-ticker.C:
		case <-e.baseCtx.Done():
			return
		}
	}
}

// runClaimed reconstructs a claimed task's request from its persisted row, re-plans it, and executes it.
// A task that has been claimed too many times (a crash loop after interruptions) is failed instead.
func (e *Engine) runClaimed(task model.Task) {
	if task.Attempts > maxAttempts() {
		_ = e.p(pidOf(task)).FinishTask(e.baseCtx, task.ID, model.TaskFailed, nil, "exceeded retry limit after interruption")
		return
	}
	req := requestFromTask(task)
	prep, err := e.prepare(e.baseCtx, req)
	if err != nil {
		_ = e.p(pidOf(task)).FinishTask(e.baseCtx, task.ID, model.TaskFailed, nil, "reconstruct: "+err.Error())
		return
	}
	_, _ = e.execute(e.baseCtx, task, req, prep)
}

// requestFromTask rebuilds a RunRequest from a persisted task row (durable-queue reconstruction).
func requestFromTask(t model.Task) RunRequest {
	req := RunRequest{
		CapabilityID:  t.CapabilityID,
		Actor:         t.Actor,
		TargetDir:     t.TargetDir,
		AssetID:       t.AssetID,
		ApplicationID: t.ApplicationID,
		ProjectID:     t.ProjectID,
		SecretRefs:    t.SecretRefs,
		RunnerID:      t.RunnerTarget,
	}
	if len(t.Params) > 0 {
		_ = json.Unmarshal(t.Params, &req.Params)
	}
	return req
}

// Cancel stops a task. A running task's container is killed and its context cancelled; a still-queued
// (pending) task is marked failed so a worker never claims it.
func (e *Engine) Cancel(taskID string) error {
	e.mu.Lock()
	rs, running := e.running[taskID]
	e.mu.Unlock()
	if running {
		if rs.container != "" {
			_ = exec.Command("docker", "kill", rs.container).Run() // best-effort
		}
		rs.cancel()
		return nil
	}
	// Not executing — cancel it if it is still queued.
	cancelled, err := e.mgr.CancelPendingTask(context.Background(), taskID)
	if err != nil || !cancelled {
		return ErrTaskNotRunning
	}
	return nil
}

// Registry exposes the capabilities this engine can run.
func (e *Engine) Registry() *capability.Registry { return e.registry }

// SetOutdatedChecker enables the deps.dev outdated-dependency enrichment with the given HTTP client
// (ADR-0031-adjacent). Without it, syft completion does not trigger a currency check.
func (e *Engine) SetOutdatedChecker(d enrich.Doer) { e.outdatedHTTP = d }

// SetOnComplete registers a callback fired after every task finishes executing (ADR-0056), used by the
// methodology runner to route a completed task's observations back to its originating item and set coverage.
func (e *Engine) SetOnComplete(fn func(ctx context.Context, oc Outcome)) { e.onComplete = fn }

// RunRequest asks the engine to run a capability against a target directory.
type RunRequest struct {
	CapabilityID  string
	Actor         string
	TargetDir     string
	AssetID       *string
	ApplicationID *string
	ProjectID     *string           // scope context for network capabilities; resolved from the asset if unset
	SecretRefs    map[string]string // envVar -> vault secret name, injected at exec time (ADR-0011)
	Params        map[string]any
	RunnerID      string // "" = local Docker runner; otherwise an enrolled remote runner id (ADR-0024)
	// MethodologyItemID / MethodologyRunID attribute the task to the methodology item + run that spawned it
	// (ADR-0056), so the on-complete hook routes results back to that item's coverage. Nil for ordinary runs.
	MethodologyItemID *string
	MethodologyRunID  *string
}

// ErrOutOfScope is returned when a network capability's target is not in the project allowlist.
var ErrOutOfScope = errors.New("task: target out of scope")

// ErrTechniqueNotPermitted is returned when a capability's rules-of-engagement technique is not allowed by
// the project's engagement (ADR-0051).
var ErrTechniqueNotPermitted = errors.New("task: technique not permitted by this engagement")

// Outcome is a completed task with its artifacts and any observations interpreted from them.
type Outcome struct {
	Task         model.Task          `json:"task"`
	Artifacts    []model.Artifact    `json:"artifacts"`
	Observations []model.Observation `json:"observations"`
}

// prepared holds everything resolved up front for a run: the capability, its planned spec, and the
// application the task belongs to. Resolving this at enqueue time means bad requests (unknown
// capability, non-source asset, plan error) fail fast before a task is created.
type prepared struct {
	man           capability.Manifest
	spec          runner.RunSpec
	applicationID *string
}

// prepare validates the request and plans the run without creating a task or touching a container.
func (e *Engine) prepare(ctx context.Context, req RunRequest) (prepared, error) {
	c, ok := e.registry.Get(req.CapabilityID)
	if !ok {
		return prepared{}, fmt.Errorf("unknown capability %q", req.CapabilityID)
	}
	man := c.Manifest()

	// Resolve the target directory from a source-repo asset when not given explicitly, and carry
	// the asset's application onto the task for provenance.
	targetDir := req.TargetDir
	applicationID := req.ApplicationID
	if targetDir == "" && req.AssetID != nil {
		asset, err := e.p(pidPtr(req.ProjectID)).GetAsset(ctx, *req.AssetID)
		if err != nil {
			return prepared{}, fmt.Errorf("resolve asset: %w", err)
		}
		if asset.Type != model.AssetSourceRepo {
			return prepared{}, fmt.Errorf("asset %s is %s; only source_repo assets have a target directory", asset.ID, asset.Type)
		}
		targetDir = asset.Location
		if applicationID == nil {
			applicationID = &asset.ApplicationID
		}
	}

	// Rules-of-engagement gate (ADR-0051): block a technique the engagement does not permit, at enqueue so
	// no task is created. applicationID is resolved above, so the project (and its engagement) is reachable.
	if err := e.checkTechnique(ctx, req, applicationID, man); err != nil {
		return prepared{}, err
	}

	spec, err := c.Plan(capability.Input{TargetDir: targetDir, Params: req.Params})
	if err != nil {
		return prepared{}, err
	}
	return prepared{man: man, spec: spec, applicationID: applicationID}, nil
}

// checkTechnique blocks a run whose capability is tagged with a technique the project's engagement does not
// permit. It fails open — no project context, no engagement, or no techniques configured means no restriction
// — so it only ever tightens once an operator has set rules of engagement.
func (e *Engine) checkTechnique(ctx context.Context, req RunRequest, applicationID *string, man capability.Manifest) error {
	if man.Technique == "" {
		return nil
	}
	projectID := ""
	switch {
	case req.ProjectID != nil:
		projectID = *req.ProjectID
	case applicationID != nil:
		if app, err := e.p(pidPtr(req.ProjectID)).GetApplication(ctx, *applicationID); err == nil {
			projectID = app.ProjectID
		}
	}
	if projectID == "" {
		return nil
	}
	eng, err := e.p(pidPtr(req.ProjectID)).GetEngagement(ctx, projectID)
	if errors.Is(err, store.ErrNotFound) {
		return nil // no engagement configured → unconstrained
	}
	if err != nil {
		return fmt.Errorf("load engagement: %w", err)
	}
	if len(eng.Techniques) == 0 {
		return nil // rules of engagement not configured → unconstrained
	}
	if !eng.Techniques[man.Technique] {
		return fmt.Errorf("%w: capability %q uses technique %q", ErrTechniqueNotPermitted, man.ID, man.Technique)
	}
	return nil
}

// createTask records a task for a prepared run. queued marks it pending (a worker starts it later).
func (e *Engine) createTask(ctx context.Context, req RunRequest, p prepared, queued bool) (model.Task, error) {
	actor := req.Actor
	if actor == "" {
		actor = "human"
	}
	paramsJSON, _ := json.Marshal(req.Params)
	return e.p(pidPtr(req.ProjectID)).CreateTask(ctx, store.NewTask{
		CapabilityID:      p.man.ID,
		CapabilityVersion: p.man.Version,
		ApplicationID:     p.applicationID,
		AssetID:           req.AssetID,
		ProjectID:         req.ProjectID,
		Actor:             actor,
		Runner:            e.runner.Name(),
		Params:            paramsJSON,
		SecretRefs:        req.SecretRefs, // reference names only, persisted for durable reconstruction
		TargetDir:         req.TargetDir,  // raw dir (empty when derived from an asset)
		RunnerTarget:      req.RunnerID,   // '' = local; else an enrolled remote runner (ADR-0024)
		MethodologyItemID: req.MethodologyItemID,
		MethodologyRunID:  req.MethodologyRunID,
		Queued:            queued,
	})
}

// SetRunnerResolver wires remote-runner selection: given a task's RunnerTarget (an enrolled runner id),
// the resolver returns the runner.Runner that dispatches to it (ADR-0024). Without it, tasks that target
// a remote runner fail cleanly.
func (e *Engine) SetRunnerResolver(fn func(runnerID string) (runner.Runner, error)) {
	e.resolveRunner = fn
}

// Enqueue validates and plans the run, records a pending task, and wakes the worker pool, returning
// immediately. The task advances to running when a worker atomically claims it from the DB (ADR-0023);
// it survives a restart because the pending row IS the queue. Bad requests fail fast (no task).
func (e *Engine) Enqueue(ctx context.Context, req RunRequest) (model.Task, error) {
	p, err := e.prepare(ctx, req)
	if err != nil {
		return model.Task{}, err
	}
	t, err := e.createTask(ctx, req, p, true)
	if err != nil {
		return model.Task{}, err
	}
	e.signal()
	return t, nil
}

// ScanResult reports what a project scan enqueued and what it skipped.
type ScanResult struct {
	Enqueued []model.Task `json:"enqueued"`
	Skipped  []ScanSkip   `json:"skipped"`
}

// ScanSkip records a capability that was not run against an asset, and why.
type ScanSkip struct {
	CapabilityID string `json:"capability_id"`
	AssetID      string `json:"asset_id"`
	Reason       string `json:"reason"`
}

// ScanProject fans out every applicable capability across the project's assets — the deterministic
// "scan everything" path, no agent involved. Each enqueued task flows through the engine's interpret →
// dedup → reachability/exposure → auto-triage pipeline on completion. Capabilities opt in by declaring
// AppliesTo; a language-specific one (Ecosystems) runs only where one of its stacks is detected. A
// per-capability enqueue error (e.g. a technique the engagement forbids, or scope) is recorded as a
// skip rather than failing the whole scan.
func (e *Engine) ScanProject(ctx context.Context, projectID string) (ScanResult, error) {
	assets, err := e.p(projectID).ListAssets(ctx)
	if err != nil {
		return ScanResult{}, err
	}
	mans := e.registry.Manifests()
	pid := projectID
	var res ScanResult
	for _, a := range assets {
		// The gate uses what we detect from the repo UNION the operator's manual tags, so a monorepo/polyglot
		// asset that root-level detection under-reads can be corrected by tagging.
		eco := capability.DetectEcosystems(a.Location)
		for _, t := range a.Ecosystems {
			eco[t] = true
		}
		for _, m := range mans {
			if !m.AppliesToKind(a.Type) {
				continue
			}
			if !m.TargetsEcosystems(eco) {
				res.Skipped = append(res.Skipped, ScanSkip{CapabilityID: m.ID, AssetID: a.ID, Reason: "no matching ecosystem (needs " + strings.Join(m.Ecosystems, "/") + ")"})
				continue
			}
			assetID := a.ID
			t, err := e.Enqueue(ctx, RunRequest{CapabilityID: m.ID, AssetID: &assetID, ProjectID: &pid, Actor: "scan-all"})
			if err != nil {
				res.Skipped = append(res.Skipped, ScanSkip{CapabilityID: m.ID, AssetID: a.ID, Reason: err.Error()})
				continue
			}
			res.Enqueued = append(res.Enqueued, t)
		}
	}
	return res, nil
}

// enrichOutdated parses a syft CycloneDX SBOM, asks deps.dev which components are behind their latest
// release, and records each as an unreviewed observation (fingerprint-deduped so a re-scan doesn't
// duplicate). Best-effort — a network or parse error just yields fewer observations.
func (e *Engine) enrichOutdated(ctx context.Context, projectID, taskID, artifactID string, sbom []byte) {
	var doc struct {
		Components []struct {
			PURL string `json:"purl"`
		} `json:"components"`
	}
	if json.Unmarshal(sbom, &doc) != nil {
		return
	}
	purls := make([]string, 0, len(doc.Components))
	for _, c := range doc.Components {
		if c.PURL != "" {
			purls = append(purls, c.PURL)
		}
	}
	comps := enrich.ComponentsFromPURLs(purls)
	if len(comps) == 0 {
		return
	}
	results := enrich.Checker{HTTP: e.outdatedHTTP}.Check(ctx, comps)
	pid := projectID
	for _, r := range results {
		o := model.Observation{
			TaskID:      &taskID,
			ArtifactID:  &artifactID,
			ProjectID:   &pid,
			Origin:      model.OriginTool,
			ReviewState: model.ReviewUnreviewed,
			Title:       "Outdated dependency: " + r.Name,
			Detail:      fmt.Sprintf("%s %s is behind the latest release %s (%s update available).", r.Name, r.Version, r.Latest, r.Drift),
			Severity:    driftSeverity(r.Drift),
			RuleID:      "outdated/" + r.Ecosystem,
			Location:    r.Name + "@" + r.Version,
			Attributes: map[string]string{
				"package": r.Name, "installed": r.Version, "latest": r.Latest, "drift": r.Drift, "outdated": "true",
			},
		}
		o.Fingerprint = interpret.Fingerprint(o)
		if _, dup := e.p(projectID).ObservationByFingerprint(ctx, projectID, o.Fingerprint); dup {
			continue
		}
		_, _ = e.p(projectID).CreateObservation(ctx, o)
	}
}

// driftSeverity maps a version drift to an OSB severity — outdated is a currency signal, not a
// vulnerability, so it stays modest (a major jump is medium at most).
func driftSeverity(drift string) string {
	switch drift {
	case "major":
		return "medium"
	case "minor":
		return "low"
	default:
		return "info"
	}
}

// Run plans the capability, executes it in the sandbox synchronously, stores its stdout as an output
// artifact in the CAS, and records the task's outcome. Provenance links artifact → task → capability+
// version → runner. Callers wanting non-blocking execution use Enqueue; Run is used where sequential,
// in-line completion is required (e.g. a playbook's ordered steps).
func (e *Engine) Run(ctx context.Context, req RunRequest) (Outcome, error) {
	p, err := e.prepare(ctx, req)
	if err != nil {
		return Outcome{}, err
	}
	task, err := e.createTask(ctx, req, p, false)
	if err != nil {
		return Outcome{}, err
	}
	return e.execute(ctx, task, req, p)
}

// execute runs a created task's container, captures its output, interprets it, and finishes the task.
// It is the shared body of both the synchronous Run and the async worker path. Every return flows through
// e.outcome, so the deferred on-complete hook (ADR-0056) fires once with the final outcome — success or
// failure — the single choke point the methodology runner needs.
func (e *Engine) execute(ctx context.Context, task model.Task, req RunRequest, p prepared) (oc Outcome, err error) {
	if e.onComplete != nil {
		defer func() { e.onComplete(ctx, oc) }()
	}
	man := p.man
	spec := p.spec

	// Scope guard: a network capability may only touch in-scope targets (P6). The task record
	// captures the blocked attempt for the audit trail.
	if man.TargetParam != "" {
		if scopeErr := e.checkScope(ctx, req, p.applicationID, man.TargetParam); scopeErr != nil {
			_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, scopeErr.Error())
			return e.outcome(ctx, pidOf(task), task.ID), scopeErr
		}
	}

	// Select the runner: local Docker by default, or an enrolled remote runner (ADR-0024). Scope was
	// already enforced above, control-plane-side, before any dispatch.
	run := e.runner
	if req.RunnerID != "" {
		if e.resolveRunner == nil {
			err := fmt.Errorf("task targets runner %q but no remote runners are configured", req.RunnerID)
			_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, err.Error())
			return e.outcome(ctx, pidOf(task), task.ID), err
		}
		r, err := e.resolveRunner(req.RunnerID)
		if err != nil {
			err = fmt.Errorf("runner unavailable: %w", err)
			_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, err.Error())
			return e.outcome(ctx, pidOf(task), task.ID), err
		}
		run = r
	}

	// Resolve secret references and inject them at exec time — never persisted, never logged; the
	// returned redactor scrubs their values from captured output (ADR-0011).
	redact, injErr := e.injectSecrets(ctx, req, &spec)
	if injErr != nil {
		_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, injErr.Error())
		return e.outcome(ctx, pidOf(task), task.ID), injErr
	}

	// Name the container and register the run so Cancel can stop it. For a remote run the container is on
	// the runner host, so leave the local container name empty (local `docker kill` is a no-op) and rely
	// on run-context cancellation propagating over the protocol.
	spec.Name = "osb-" + task.ID
	localContainer := spec.Name
	if req.RunnerID != "" {
		localContainer = ""
	}
	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[task.ID] = runState{cancel: cancel, container: localContainer}
	e.mu.Unlock()

	res, runErr := run.Run(runCtx, spec)

	e.mu.Lock()
	delete(e.running, task.ID)
	e.mu.Unlock()
	cancel()

	// Scrub any injected secret values from captured output before it is stored anywhere.
	res.Stdout = redact(res.Stdout)
	res.Stderr = redact(res.Stderr)

	if runErr != nil {
		msg := runErr.Error()
		if runCtx.Err() == context.Canceled {
			msg = "cancelled by user"
		}
		// FinishTask uses the original ctx (still live); runCtx may be cancelled.
		_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, msg)
		return e.outcome(ctx, pidOf(task), task.ID), runErr
	}

	// Capture stdout as the primary output artifact (immutable, content-addressed).
	digest, err := e.casFor(pidOf(task)).Put(bytes.NewReader(res.Stdout))
	if err != nil {
		_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, "store artifact: "+err.Error())
		return e.outcome(ctx, pidOf(task), task.ID), err
	}
	art, err := e.p(pidOf(task)).CreateArtifact(ctx, model.Artifact{
		TaskID:    &task.ID,
		SHA256:    digest,
		Size:      int64(len(res.Stdout)),
		Kind:      model.ArtifactOutput,
		Name:      man.OutputName,
		MediaType: man.OutputMediaType,
	})
	if err != nil {
		_ = e.p(pidOf(task)).FinishTask(ctx, task.ID, model.TaskFailed, nil, "record artifact: "+err.Error())
		return e.outcome(ctx, pidOf(task), task.ID), err
	}

	// Deterministically interpret recognized output formats into unreviewed observations
	// (ADR-0005). Interpretation failures do not fail the task — the raw artifact is still evidence.
	var interpreted []model.Observation
	switch man.OutputMediaType {
	case interpret.SARIFMediaType:
		interpreted, _ = interpret.SARIF(res.Stdout)
	case interpret.NmapMediaType:
		interpreted, _ = interpret.NmapXML(res.Stdout)
	case interpret.TruffleHogMediaType:
		interpreted, _ = interpret.TruffleHog(res.Stdout)
	case interpret.GovulncheckMediaType:
		interpreted, _ = interpret.Govulncheck(res.Stdout)
	}
	// Resolve the project once: it scopes both fingerprint dedup and disposition routing.
	projectID := e.projectOfTask(ctx, task, p.applicationID)
	// Route-map output is an entry-point inventory, not observations (ADR-0033): upsert the declared routes
	// and confirm their exposure against captured traffic, then we're done — there are no observations.
	if man.OutputMediaType == interpret.RouteMediaType && projectID != "" {
		if routes, rerr := interpret.Routes(res.Stdout); rerr == nil {
			for _, r := range routes {
				r.ProjectID = projectID
				_ = e.p(pidOf(task)).UpsertRoute(ctx, r)
			}
			_ = e.p(pidOf(task)).ReconcileObservedRoutes(ctx, projectID)
		}
	}
	// Derive the exposed-service signal once per run (ADR-0030): reachability-gated routing escalates only
	// findings on a network-exposed service, so every new observation is tagged with the project's exposure.
	// Only when there are observations to enrich — a capability with no interpreter does no extra DB work.
	exposedAttr := ""
	if projectID != "" && len(interpreted) > 0 {
		if exp, err := e.p(pidOf(task)).ProjectExposure(ctx, projectID); err == nil {
			exposedAttr = strconv.FormatBool(exp.Exposed)
		}
	}
	var created []model.Observation
	for _, o := range interpreted {
		o.TaskID = &task.ID
		o.ArtifactID = &art.ID
		// The exposure signal is derived once per run above; hand it to the shared ingest so it isn't
		// re-queried per observation. IngestObservation does the dedup, cross-tool merge, and enrichment
		// (ADR-0029/0037) — the same path the agent's create_observation uses, so both feed one dataset.
		if projectID != "" && exposedAttr != "" {
			if o.Attributes == nil {
				o.Attributes = map[string]string{}
			}
			o.Attributes["exposed"] = exposedAttr
		}
		// Only newly-created observations are dispositioned; a deduped/merged one keeps its existing triage.
		if saved, isNew, err := e.IngestObservation(ctx, projectID, o); err == nil && isNew {
			created = append(created, saved)
		}
	}
	// Route new observations to a post-run disposition (ADR-0028): auto-finding, investigate, or review.
	if len(created) > 0 {
		e.applyDispositions(ctx, projectID, man, p.applicationID, created)
	}

	// Outdated-dependency enrichment (deps.dev): a syft SBOM is the currency signal's input. Flag
	// components behind their latest release as observations so they join the vuln signal on the graph.
	// Async + best-effort; only when a checker is configured (skipped in tests / offline).
	if man.ID == "syft" && projectID != "" && e.outdatedHTTP != nil {
		sbom := append([]byte(nil), res.Stdout...)
		taskID, artID := task.ID, art.ID
		go e.enrichOutdated(context.Background(), projectID, taskID, artID, sbom)
	}

	// Retroactive re-evaluation (ADR-0034): a capability that changes correlation inputs — routes
	// (route-map), the shared reachability verdict (govulncheck), or network exposure (nmap/http-probe) —
	// can make an EARLIER finding exploitable. Re-run correlation + disposition over existing observations
	// so a route or reachability verdict that arrives after a finding was recorded still upgrades it.
	if projectID != "" && reEvalTrigger(man.ID) {
		pid := projectID
		go e.ReEvaluate(context.Background(), pid)
	}

	status := model.TaskSucceeded
	errMsg := ""
	if !man.ExitOK(res.ExitCode) {
		status = model.TaskFailed
		errMsg = fmt.Sprintf("exit %d: %s", res.ExitCode, tail(res.Stderr, 500))
	}
	code := res.ExitCode
	if err := e.p(pidOf(task)).FinishTask(ctx, task.ID, status, &code, errMsg); err != nil {
		return e.outcome(ctx, pidOf(task), task.ID), err
	}
	return e.outcome(ctx, pidOf(task), task.ID), nil
}

// IngestObservation records one observation into a project's unified dataset with the enrichment and dedup
// every source shares, so scanner output and agent-authored observations land in the *same* pool: exposure/
// reachability correlation, content-fingerprint dedup (an existing match is refreshed, not duplicated), and
// cross-tool vuln merge (the same CVE from a second source folds into the first). It deliberately does NOT
// run dispositions — routing is the caller's choice: the scanner auto-routes new observations, an agent's
// judgment goes to the human triage queue. Returns the saved observation and whether it was newly created
// (false = deduped or merged into an existing one). With no project scope it stores the row as-is.
//
// This is the single ingest path; callers must not re-implement dedup/merge/enrichment, or the human and
// agent views drift apart.
func (e *Engine) IngestObservation(ctx context.Context, projectID string, o model.Observation) (model.Observation, bool, error) {
	db := e.p(projectID)
	if projectID == "" {
		saved, err := db.CreateObservation(ctx, o)
		return saved, err == nil, err
	}
	o.ProjectID = &projectID
	o.Fingerprint = interpret.Fingerprint(o) // computed before enrichment; Fingerprint excludes attributes
	// Exposure signal: reuse a caller-provided value (the scanner derives it once per run), else derive it.
	exposedAttr := ""
	if o.Attributes != nil {
		exposedAttr = o.Attributes["exposed"]
	}
	if exposedAttr == "" {
		if exp, err := db.ProjectExposure(ctx, projectID); err == nil {
			exposedAttr = strconv.FormatBool(exp.Exposed)
			if o.Attributes == nil {
				o.Attributes = map[string]string{}
			}
			o.Attributes["exposed"] = exposedAttr
		}
	}
	e.correlateReachability(ctx, projectID, &o)
	e.correlateExposedRoute(ctx, projectID, exposedAttr, &o)

	// Content-fingerprint dedup (ADR-0029): an already-recorded finding is refreshed, not duplicated.
	if existingID, dup := db.ObservationByFingerprint(ctx, projectID, o.Fingerprint); dup {
		_ = db.RefreshObservation(ctx, existingID, o.Severity, o.Detail, o.Attributes)
		existing, err := db.GetObservation(ctx, existingID)
		return existing, false, err
	}
	// Cross-tool merge (ADR-0037): the same vulnerability under a different advisory id folds into the first.
	if ids := vulnIDs(&o); len(ids) > 0 {
		if existingID, dup := db.ObservationForVuln(ctx, projectID, ids); dup {
			e.mergeVulnObservation(ctx, projectID, existingID, &o, ids)
			existing, err := db.GetObservation(ctx, existingID)
			return existing, false, err
		}
	}
	saved, err := db.CreateObservation(ctx, o)
	if err != nil {
		return model.Observation{}, false, err
	}
	if ids := vulnIDs(&saved); len(ids) > 0 {
		_ = db.RecordObservationVulns(ctx, projectID, saved.ID, ids)
	}
	e.recordReachabilityFacts(ctx, projectID, &saved)
	return saved, true, nil
}

// mergeVulnObservation folds a second tool's report of a vulnerability into the observation that already
// owns it (ADR-0037): it records the additional tool, unions the advisory ids, keeps the higher severity,
// and adopts a reachability verdict if the first tool had none. The duplicate is never created, and no
// second disposition fires — the merged observation keeps the first one's triage.
func (e *Engine) mergeVulnObservation(ctx context.Context, projectID, existingID string, o *model.Observation, newIDs []string) {
	existing, err := e.p(projectID).GetObservation(ctx, existingID)
	if err != nil {
		return
	}
	attrs := existing.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["tools"] = unionCSV(firstNonBlank(attrs["tools"], attrs["tool"]), firstNonBlank(o.Attributes["tools"], o.Attributes["tool"]))
	attrs["aliases"] = unionCSV(attrs["aliases"], strings.Join(newIDs, ","))
	if attrs["reachable"] == "" && o.Attributes["reachable"] != "" {
		attrs["reachable"] = o.Attributes["reachable"]
	}
	sev := existing.Severity
	if severityRank(o.Severity) > severityRank(sev) {
		sev = o.Severity
	}
	_ = e.p(projectID).RefreshObservation(ctx, existingID, sev, existing.Detail, attrs)
	// Claim the second tool's ids for the existing observation, so a third tool with yet another id merges too.
	_ = e.p(projectID).RecordObservationVulns(ctx, projectID, existingID, newIDs)
}

var severityOrder = map[string]int{"info": 0, "low": 1, "medium": 2, "high": 3, "critical": 4}

func severityRank(s string) int { return severityOrder[s] }

func firstNonBlank(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// unionCSV merges two comma-separated sets into one, order-stable, de-duplicated, blanks dropped.
func unionCSV(a, b string) string {
	seen := map[string]bool{}
	var out []string
	for _, part := range append(strings.Split(a, ","), strings.Split(b, ",")...) {
		part = strings.TrimSpace(part)
		if part == "" || seen[part] {
			continue
		}
		seen[part] = true
		out = append(out, part)
	}
	return strings.Join(out, ",")
}

// recordReachabilityFacts emits reachability facts from an observation's correlated attributes into the
// aggregation store (ADR-0031/0034): govulncheck's call-graph verdict (per CVE, proven), a taint dataflow
// trace (per observation, medium), and a proven route→sink path (per observation, high). Human/LLM facts
// are added through their own paths. Best-effort.
func (e *Engine) recordReachabilityFacts(ctx context.Context, projectID string, o *model.Observation) {
	a := o.Attributes
	if a == nil {
		return
	}
	// govulncheck: a sound call-graph verdict on a CVE (reachable OR unreachable) — proven for Go.
	if a["tool"] == "govulncheck" && a["reachable"] != "" {
		verdict := model.ReachUnreachable
		if a["reachable"] == "true" {
			verdict = model.ReachReachable
		}
		for _, id := range vulnIDs(o) {
			_ = e.p(projectID).AddReachabilityFact(ctx, model.ReachabilityFact{
				ProjectID: projectID, SubjectType: model.ReachSubjectCVE, SubjectKey: id,
				Reachable: verdict, Confidence: model.ReachConfProven, Source: "govulncheck",
				Method: "static call graph", Rationale: a["package"],
			})
		}
	}
	// SAST taint dataflow: the sink is reachable from an untrusted source — heuristic (medium).
	if a["dataflow_source"] != "" || a["dataflow_path"] != "" {
		src := a["tool"]
		if src == "" {
			src = "opengrep"
		}
		_ = e.p(projectID).AddReachabilityFact(ctx, model.ReachabilityFact{
			ProjectID: projectID, SubjectType: model.ReachSubjectObservation, SubjectKey: o.ID,
			Reachable: model.ReachReachable, Confidence: model.ReachConfMedium, Source: src,
			Method: "taint dataflow", Rationale: "source: " + a["dataflow_source"],
		})
	}
	// route→sink: a proven path from an HTTP entry point to the sink (high).
	if a["route_reachable"] == "true" {
		_ = e.p(projectID).AddReachabilityFact(ctx, model.ReachabilityFact{
			ProjectID: projectID, SubjectType: model.ReachSubjectObservation, SubjectKey: o.ID,
			Reachable: model.ReachReachable, Confidence: model.ReachConfHigh, Source: "route-analysis",
			Method: "route→sink dataflow", Rationale: "entry point: " + a["exposed_route"],
		})
	}
}

var reachConfRank = map[string]int{
	model.ReachConfLow: 1, model.ReachConfMedium: 2, model.ReachConfHigh: 3, model.ReachConfProven: 4,
}

// applyAggregateReachability folds the resolved reachability verdict (across all sources — tools, traffic,
// and any manual/LLM fact) back onto an observation's attributes, so disposition and the UI see the
// aggregate rather than a single tool's opinion. reachable_confirmed marks a high-confidence reachable
// verdict (a sound tool, or a human/LLM who verified it) — the signal that escalates on its own.
func (e *Engine) applyAggregateReachability(ctx context.Context, projectID string, o *model.Observation) {
	verdict, conf, _ := e.p(projectID).ResolveReachability(ctx, projectID, model.ReachSubjectObservation, o.ID)
	// A CVE-subject verdict (e.g. govulncheck's, or a manual verdict on the CVE) applies too — take the
	// strongest across the observation and its advisory ids; on a tie, reachable wins.
	for _, id := range vulnIDs(o) {
		v, c, _ := e.p(projectID).ResolveReachability(ctx, projectID, model.ReachSubjectCVE, id)
		if reachConfRank[c] > reachConfRank[conf] || (reachConfRank[c] == reachConfRank[conf] && v == model.ReachReachable) {
			verdict, conf = v, c
		}
	}
	if verdict == model.ReachUnknown || conf == "" {
		return
	}
	if o.Attributes == nil {
		o.Attributes = map[string]string{}
	}
	o.Attributes["reachable"] = strconv.FormatBool(verdict == model.ReachReachable)
	o.Attributes["reachable_confidence"] = conf
	if verdict == model.ReachReachable && (conf == model.ReachConfProven || conf == model.ReachConfHigh) {
		o.Attributes["reachable_confirmed"] = "true"
	} else {
		delete(o.Attributes, "reachable_confirmed")
	}
}

// reEvalTrigger reports whether finishing this capability changed a correlation input (routes, the shared
// reachability verdict, or network exposure) and therefore warrants re-evaluating existing observations.
func reEvalTrigger(capID string) bool {
	switch capID {
	case "route-map", "govulncheck", "nmap", "http-probe":
		return true
	}
	return false
}

// ReEvaluate re-runs correlation (exposure, reachability, route→sink) and disposition over the project's
// existing observations, so a finding recorded before the data that makes it exploitable arrived — a
// route discovered later, or a govulncheck reachability verdict for a CVE grype already reported — is
// upgraded retroactively (ADR-0034). Only still-unreviewed observations are (re)dispositioned; human
// triage is never overridden. Best-effort; serialized so concurrent triggers can't double-promote.
func (e *Engine) ReEvaluate(ctx context.Context, projectID string) {
	if projectID == "" {
		return
	}
	e.reEvalMu.Lock()
	defer e.reEvalMu.Unlock()

	obs, err := e.p(projectID).ListObservationsByProject(ctx, projectID)
	if err != nil || len(obs) == 0 {
		return
	}
	exposedAttr := ""
	if exp, err := e.p(projectID).ProjectExposure(ctx, projectID); err == nil {
		exposedAttr = strconv.FormatBool(exp.Exposed)
	}
	for i := range obs {
		o := obs[i]
		before := attrsKey(o.Attributes)
		if o.Attributes == nil {
			o.Attributes = map[string]string{}
		}
		if exposedAttr != "" {
			o.Attributes["exposed"] = exposedAttr
		}
		e.correlateReachability(ctx, projectID, &o)
		e.correlateExposedRoute(ctx, projectID, exposedAttr, &o)
		// Fold the aggregated reachability verdict (including any manual/LLM fact) back onto the observation,
		// so a human/LLM "reachable" determination flows into disposition and display.
		e.applyAggregateReachability(ctx, projectID, &o)
		if attrsKey(o.Attributes) != before {
			_ = e.p(projectID).RefreshObservation(ctx, o.ID, o.Severity, o.Detail, o.Attributes)
		}
		// Retroactive escalation: an unreviewed observation whose disposition now fires promotes to a
		// finding/investigation. Already-triaged (confirmed/rejected) observations are left alone.
		if o.ReviewState == model.ReviewUnreviewed && o.TaskID != nil {
			if task, err := e.p(projectID).GetTask(ctx, *o.TaskID); err == nil {
				if c, ok := e.registry.Get(task.CapabilityID); ok {
					e.applyDispositions(ctx, projectID, c.Manifest(), task.ApplicationID, []model.Observation{o})
				}
			}
		}
	}
}

// attrsKey is a stable serialization of an attribute map, for detecting whether re-correlation changed it.
func attrsKey(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(m[k])
		b.WriteByte(';')
	}
	return b.String()
}

// applyDispositions routes each new observation to a post-run action per the capability's declared
// dispositions and any project overrides (ADR-0028): promote to a finding, open an investigation, or
// leave for manual review. Best-effort — a routing failure never fails the task.
func (e *Engine) applyDispositions(ctx context.Context, projectID string, man capability.Manifest, appID *string, observations []model.Observation) {
	rules := e.dispositionRules(ctx, projectID, man)
	if len(rules) == 0 {
		return
	}
	for _, o := range observations {
		switch disposition.Evaluate(o, rules) {
		case disposition.ActionFinding:
			if err := e.p(projectID).ReviewObservation(ctx, o.ID, model.ReviewConfirmed); err != nil {
				continue
			}
			f, err := e.p(projectID).CreateFinding(ctx, store.NewFinding{
				ApplicationID: appID, Title: o.Title, Severity: o.Severity,
				Description: o.Detail, ObservationIDs: []string{o.ID},
			})
			if err == nil {
				e.auditDisposition(ctx, "disposition.finding", f.ID, o)
			}
		case disposition.ActionInvestigate:
			if projectID == "" {
				continue // an investigation must be project-scoped
			}
			// Cross-tool dedup (ADR-0037): if this vulnerability (by CVE/GHSA) is already under
			// investigation — e.g. govulncheck opened it and grype now reports the same CVE under its GHSA —
			// don't open a second one.
			ids := vulnIDs(&o)
			if _, dup := e.p(projectID).InvestigationForVuln(ctx, projectID, ids); dup {
				continue
			}
			inv, err := e.p(projectID).CreateInvestigation(ctx, model.Investigation{
				ProjectID: projectID, ApplicationID: appID, ObservationID: o.ID, Title: o.Title,
			})
			if err == nil {
				_ = e.p(projectID).RecordInvestigationVulns(ctx, projectID, inv.ID, ids)
				e.auditDisposition(ctx, "disposition.investigate", inv.ID, o)
			}
		}
	}
}

// dispositionRules merges the project's overrides (higher priority) with the capability's manifest
// defaults into an ordered rule list for Evaluate.
func (e *Engine) dispositionRules(ctx context.Context, projectID string, man capability.Manifest) []disposition.Disposition {
	var rules []disposition.Disposition
	if projectID != "" {
		overrides, _ := e.p(projectID).ListDispositionRules(ctx, projectID)
		for _, pr := range overrides {
			if pr.CapabilityID == "" || pr.CapabilityID == man.ID {
				rules = append(rules, disposition.Disposition{When: pr.When, MinSeverity: pr.MinSeverity, Action: pr.Action})
			}
		}
	}
	return append(rules, man.Dispositions...)
}

func (e *Engine) projectOfTask(ctx context.Context, task model.Task, appID *string) string {
	if task.ProjectID != nil {
		return *task.ProjectID
	}
	if appID != nil {
		if app, err := e.p(pidOf(task)).GetApplication(ctx, *appID); err == nil {
			return app.ProjectID
		}
	}
	return ""
}

var cveRe = regexp.MustCompile(`CVE-\d{4}-\d{4,}`)
var ghsaRe = regexp.MustCompile(`GHSA(-[0-9a-z]{4}){3}`)

// vulnIDs returns the advisory identifiers (CVE / GHSA) an observation is about — pulled from its rule id
// and its `aliases` attribute. Different SCA tools key by different schemes (grype → GHSA, govulncheck →
// CVE), so correlation records/looks up under all of them (ADR-0031).
func vulnIDs(o *model.Observation) []string {
	var ids []string
	seen := map[string]bool{}
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			ids = append(ids, s)
		}
	}
	add(cveRe.FindString(o.RuleID))
	add(ghsaRe.FindString(o.RuleID))
	for _, a := range strings.Split(o.Attributes["aliases"], ",") {
		a = strings.TrimSpace(a)
		if cveRe.MatchString(a) || ghsaRe.MatchString(a) {
			add(a)
		}
	}
	// Also scan the finding text: SCA tools cross-reference the other schemes in their message (grype names
	// the CVE alongside its GHSA rule id, and vice-versa), so extracting both lets a grype GHSA finding match
	// an osv/govulncheck CVE finding for the same vulnerability (ADR-0031/0037).
	text := o.Title + " " + o.Detail
	for _, m := range cveRe.FindAllString(text, -1) {
		add(m)
	}
	for _, m := range ghsaRe.FindAllString(text, -1) {
		add(m)
	}
	return ids
}

// correlateReachability ties a dependency-vuln observation to the project's shared reachability verdicts
// (ADR-0031). If the observation carries its own `reachable` attribute (from an analyzer like govulncheck),
// its verdict is recorded under every advisory id it knows, so a tool keying by a different scheme can find
// it. Otherwise a vuln finding from a tool without reachability (e.g. grype) inherits any stored verdict for
// one of its ids. Best-effort.
func (e *Engine) correlateReachability(ctx context.Context, projectID string, o *model.Observation) {
	ids := vulnIDs(o)
	if len(ids) == 0 {
		return
	}
	if own := o.Attributes["reachable"]; own != "" {
		for _, id := range ids {
			_ = e.p(projectID).SetReachability(ctx, projectID, id, o.Attributes["package"], own == "true", o.Attributes["tool"])
		}
		return
	}
	for _, id := range ids {
		if reachable, known := e.p(projectID).ReachabilityForCVE(ctx, projectID, id); known {
			if o.Attributes == nil {
				o.Attributes = map[string]string{}
			}
			o.Attributes["reachable"] = strconv.FormatBool(reachable)
			return
		}
	}
}

// correlateExposedRoute ties a source finding to a specific exposed HTTP entry point (ADR-0033): when the
// finding's location file declares a route, and that route is exposed (traffic-confirmed, or the service is
// exposed at all), it sets `exposed_route` ("METHOD /path") and `route_observed`. This refines the coarse
// project-level `exposed` to a concrete route. File-level proximity — the finding sits in the handler file,
// not proven reachable from the route by a call graph. Best-effort; a nil route inventory just skips it.
func (e *Engine) correlateExposedRoute(ctx context.Context, projectID, exposedAttr string, o *model.Observation) {
	// Call-graph route→sink reachability first (ADR-0034): opengrep's dataflow trace lists every location
	// from the untrusted source to the sink. A route handler anywhere on that path proves the sink is
	// reachable from that HTTP entry point — the strong, path-based signal (route_reachable).
	if e.correlateDataflowRoute(ctx, projectID, exposedAttr, o) {
		return
	}
	// Fallback (ADR-0033): the sink itself sits in a route handler's file. Weaker — co-location, not a
	// traced path — so it sets exposed_route/route_observed but not route_reachable.
	file, line := splitLocation(o.Location)
	if file == "" {
		return
	}
	routes, err := e.p(projectID).RoutesForHandlerFile(ctx, projectID, file)
	if err != nil || len(routes) == 0 {
		return
	}
	// Pick the route whose handler actually contains the finding — the nearest route registration at or above
	// the finding's line (a file can declare several routes). Falls back to the first route when the finding
	// has no line or precedes every route.
	r := nearestRoute(routes, line)
	if !r.Observed && exposedAttr != "true" {
		return
	}
	setRouteAttrs(o, r, false)
}

// correlateDataflowRoute walks the finding's recorded source→sink dataflow path and, if a route handler
// sits on it, attributes that route and marks the finding route_reachable. Returns true when it matched.
func (e *Engine) correlateDataflowRoute(ctx context.Context, projectID, exposedAttr string, o *model.Observation) bool {
	raw := o.Attributes["dataflow_path"]
	if raw == "" {
		return false
	}
	for _, loc := range strings.Split(raw, ",") {
		file, line := splitLocation(loc)
		if file == "" {
			continue
		}
		routes, err := e.p(projectID).RoutesForHandlerFile(ctx, projectID, file)
		if err != nil || len(routes) == 0 {
			continue
		}
		r := nearestRoute(routes, line)
		if !r.Observed && exposedAttr != "true" {
			continue
		}
		setRouteAttrs(o, r, true)
		return true
	}
	return false
}

// setRouteAttrs tags an observation with the entry-point route. reachable marks a traced call-graph path
// from that route to the sink (route_reachable), as opposed to mere sink-in-handler-file co-location.
func setRouteAttrs(o *model.Observation, r model.Route, reachable bool) {
	if o.Attributes == nil {
		o.Attributes = map[string]string{}
	}
	o.Attributes["exposed_route"] = strings.TrimSpace(r.Method + " " + r.Path)
	o.Attributes["route_observed"] = strconv.FormatBool(r.Observed)
	if reachable {
		o.Attributes["route_reachable"] = "true"
	}
}

// nearestRoute returns the route whose registration is closest at or above the finding's line — i.e. the
// handler the finding sits in. With no line (0) or a finding above all routes, it returns the first route.
func nearestRoute(routes []model.Route, line int) model.Route {
	best := routes[0]
	if line <= 0 {
		return best
	}
	bestLine := -1
	for _, r := range routes {
		if r.HandlerLine <= line && r.HandlerLine > bestLine {
			bestLine = r.HandlerLine
			best = r
		}
	}
	return best
}

// splitLocation parses an observation location "file:line" into its parts, stripping only a trailing
// :<digits>. A bare path, or an nmap "host:port/proto", returns (loc, 0) — it won't match a route file.
func splitLocation(loc string) (file string, line int) {
	i := strings.LastIndex(loc, ":")
	if i <= 0 {
		return loc, 0
	}
	n, err := strconv.Atoi(loc[i+1:])
	if err != nil {
		return loc, 0 // not a pure line number (e.g. host:port/proto)
	}
	return loc[:i], n
}

func (e *Engine) auditDisposition(ctx context.Context, action, target string, o model.Observation) {
	data, _ := json.Marshal(map[string]string{"observation": o.ID, "rule": o.RuleID})
	_, _ = e.g().AppendAudit(ctx, "disposition", action, target, data)
}

// injectSecrets resolves req.SecretRefs through the vault into spec.SecretEnv and returns a redactor
// that scrubs those values from bytes. With no refs it is a no-op; refs without a configured vault
// resolver are an error (fail closed rather than run without the requested secrets).
func (e *Engine) injectSecrets(ctx context.Context, req RunRequest, spec *runner.RunSpec) (func([]byte) []byte, error) {
	noop := func(b []byte) []byte { return b }
	if len(req.SecretRefs) == 0 {
		return noop, nil
	}
	if e.Secrets == nil {
		return nil, errors.New("task: secret injection requested but no vault is configured")
	}
	if spec.SecretEnv == nil {
		spec.SecretEnv = make(map[string]string, len(req.SecretRefs))
	}
	type rep struct{ val, name string }
	var reps []rep
	for env, name := range req.SecretRefs {
		val, err := e.Secrets(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("resolve secret %q: %w", name, err)
		}
		spec.SecretEnv[env] = val
		if val != "" {
			reps = append(reps, rep{val, name})
		}
	}
	return func(b []byte) []byte {
		for _, r := range reps {
			b = bytes.ReplaceAll(b, []byte(r.val), []byte("«redacted:"+r.name+"»"))
		}
		return b
	}, nil
}

// checkScope resolves the project for a run and enforces its in-scope allowlist against the
// target param. No project context or an empty allowlist means no restriction (allow).
func (e *Engine) checkScope(ctx context.Context, req RunRequest, applicationID *string, targetParam string) error {
	projectID := ""
	switch {
	case req.ProjectID != nil:
		projectID = *req.ProjectID
	case applicationID != nil:
		if app, err := e.p(pidPtr(req.ProjectID)).GetApplication(ctx, *applicationID); err == nil {
			projectID = app.ProjectID
		}
	}
	if projectID == "" {
		return nil
	}
	entries, err := e.p(pidPtr(req.ProjectID)).ListScopeEntries(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load scope: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	target, _ := req.Params[targetParam].(string)
	rules := make([]scope.Entry, len(entries))
	for i, en := range entries {
		rules[i] = scope.Entry{Kind: en.Kind, Value: en.Value, Disposition: en.Disposition}
	}
	if err := scope.Check(rules, target); err != nil {
		return fmt.Errorf("%w: %v", ErrOutOfScope, err)
	}
	return nil
}

func (e *Engine) outcome(ctx context.Context, projectID, taskID string) Outcome {
	t, _ := e.p(projectID).GetTask(ctx, taskID)
	a, _ := e.p(projectID).ListArtifactsByTask(ctx, taskID)
	o, _ := e.p(projectID).ListObservationsByTask(ctx, taskID)
	return Outcome{Task: t, Artifacts: a, Observations: o}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
