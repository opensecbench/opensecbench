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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/disposition"
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
	store    *store.DB
	blobs    *cas.Store
	registry *capability.Registry
	runner   runner.Runner

	// Secrets, if set, resolves a vault secret name to its plaintext for exec-time injection
	// (ADR-0011). nil disables secret injection.
	Secrets func(ctx context.Context, name string) (string, error)

	// resolveRunner, if set, returns the runner for a task's RunnerTarget (a remote runner id). When a
	// task targets a remote runner and this is nil or errors, the task fails cleanly (ADR-0024).
	resolveRunner func(runnerID string) (runner.Runner, error)

	notify     chan struct{}   // "there may be work" wakeup, so workers claim immediately on enqueue
	baseCtx    context.Context // parent context for background runs (survives request cancellation)
	baseCancel context.CancelFunc
	wg         sync.WaitGroup // worker goroutines

	mu      sync.Mutex
	running map[string]runState
}

type runState struct {
	cancel    context.CancelFunc
	container string
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
func NewEngine(st *store.DB, blobs *cas.Store, reg *capability.Registry, r runner.Runner) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	workers := workerCount()
	e := &Engine{
		store: st, blobs: blobs, registry: reg, runner: r,
		notify:     make(chan struct{}, workers),
		baseCtx:    ctx,
		baseCancel: cancel,
		running:    make(map[string]runState),
	}
	if st != nil {
		if n, err := st.RequeueInterruptedTasks(ctx); err == nil && n > 0 {
			log.Printf("task: requeued %d interrupted task(s) on startup", n)
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
		task, ok, err := e.store.ClaimNextPendingTask(e.baseCtx)
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
		_ = e.store.FinishTask(e.baseCtx, task.ID, model.TaskFailed, nil, "exceeded retry limit after interruption")
		return
	}
	req := requestFromTask(task)
	prep, err := e.prepare(e.baseCtx, req)
	if err != nil {
		_ = e.store.FinishTask(e.baseCtx, task.ID, model.TaskFailed, nil, "reconstruct: "+err.Error())
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
	cancelled, err := e.store.CancelPendingTask(context.Background(), taskID)
	if err != nil || !cancelled {
		return ErrTaskNotRunning
	}
	return nil
}

// Registry exposes the capabilities this engine can run.
func (e *Engine) Registry() *capability.Registry { return e.registry }

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
}

// ErrOutOfScope is returned when a network capability's target is not in the project allowlist.
var ErrOutOfScope = errors.New("task: target out of scope")

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
		asset, err := e.store.GetAsset(ctx, *req.AssetID)
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

	spec, err := c.Plan(capability.Input{TargetDir: targetDir, Params: req.Params})
	if err != nil {
		return prepared{}, err
	}
	return prepared{man: man, spec: spec, applicationID: applicationID}, nil
}

// createTask records a task for a prepared run. queued marks it pending (a worker starts it later).
func (e *Engine) createTask(ctx context.Context, req RunRequest, p prepared, queued bool) (model.Task, error) {
	actor := req.Actor
	if actor == "" {
		actor = "human"
	}
	paramsJSON, _ := json.Marshal(req.Params)
	return e.store.CreateTask(ctx, store.NewTask{
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
// It is the shared body of both the synchronous Run and the async worker path.
func (e *Engine) execute(ctx context.Context, task model.Task, req RunRequest, p prepared) (Outcome, error) {
	man := p.man
	spec := p.spec

	// Scope guard: a network capability may only touch in-scope targets (P6). The task record
	// captures the blocked attempt for the audit trail.
	if man.TargetParam != "" {
		if scopeErr := e.checkScope(ctx, req, p.applicationID, man.TargetParam); scopeErr != nil {
			_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, scopeErr.Error())
			return e.outcome(ctx, task.ID), scopeErr
		}
	}

	// Select the runner: local Docker by default, or an enrolled remote runner (ADR-0024). Scope was
	// already enforced above, control-plane-side, before any dispatch.
	run := e.runner
	if req.RunnerID != "" {
		if e.resolveRunner == nil {
			err := fmt.Errorf("task targets runner %q but no remote runners are configured", req.RunnerID)
			_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, err.Error())
			return e.outcome(ctx, task.ID), err
		}
		r, err := e.resolveRunner(req.RunnerID)
		if err != nil {
			err = fmt.Errorf("runner unavailable: %w", err)
			_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, err.Error())
			return e.outcome(ctx, task.ID), err
		}
		run = r
	}

	// Resolve secret references and inject them at exec time — never persisted, never logged; the
	// returned redactor scrubs their values from captured output (ADR-0011).
	redact, injErr := e.injectSecrets(ctx, req, &spec)
	if injErr != nil {
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, injErr.Error())
		return e.outcome(ctx, task.ID), injErr
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
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, msg)
		return e.outcome(ctx, task.ID), runErr
	}

	// Capture stdout as the primary output artifact (immutable, content-addressed).
	digest, err := e.blobs.Put(bytes.NewReader(res.Stdout))
	if err != nil {
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, "store artifact: "+err.Error())
		return e.outcome(ctx, task.ID), err
	}
	art, err := e.store.CreateArtifact(ctx, model.Artifact{
		TaskID:    &task.ID,
		SHA256:    digest,
		Size:      int64(len(res.Stdout)),
		Kind:      model.ArtifactOutput,
		Name:      man.OutputName,
		MediaType: man.OutputMediaType,
	})
	if err != nil {
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, "record artifact: "+err.Error())
		return e.outcome(ctx, task.ID), err
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
				_ = e.store.UpsertRoute(ctx, r)
			}
			_ = e.store.ReconcileObservedRoutes(ctx, projectID)
		}
	}
	// Derive the exposed-service signal once per run (ADR-0030): reachability-gated routing escalates only
	// findings on a network-exposed service, so every new observation is tagged with the project's exposure.
	// Only when there are observations to enrich — a capability with no interpreter does no extra DB work.
	exposedAttr := ""
	if projectID != "" && len(interpreted) > 0 {
		if exp, err := e.store.ProjectExposure(ctx, projectID); err == nil {
			exposedAttr = strconv.FormatBool(exp.Exposed)
		}
	}
	var created []model.Observation
	for _, o := range interpreted {
		o.TaskID = &task.ID
		o.ArtifactID = &art.ID
		// Content-fingerprint dedup (ADR-0029): if we've already recorded this finding in the project,
		// skip it — no duplicate observation and no repeated disposition (which would re-open an
		// investigation / re-seed an agent thread and burn tokens on a finding we've already seen).
		if projectID != "" {
			o.ProjectID = &projectID
			o.Fingerprint = interpret.Fingerprint(o) // computed before enrichment; excludes attributes anyway
			if _, dup := e.store.ObservationByFingerprint(ctx, projectID, o.Fingerprint); dup {
				continue
			}
			if exposedAttr != "" {
				if o.Attributes == nil {
					o.Attributes = map[string]string{}
				}
				o.Attributes["exposed"] = exposedAttr
			}
			e.correlateReachability(ctx, projectID, &o)
			e.correlateExposedRoute(ctx, projectID, exposedAttr, &o)
		}
		if saved, err := e.store.CreateObservation(ctx, o); err == nil {
			created = append(created, saved)
		}
	}
	// Route new observations to a post-run disposition (ADR-0028): auto-finding, investigate, or review.
	if len(created) > 0 {
		e.applyDispositions(ctx, projectID, man, p.applicationID, created)
	}

	status := model.TaskSucceeded
	errMsg := ""
	if !man.ExitOK(res.ExitCode) {
		status = model.TaskFailed
		errMsg = fmt.Sprintf("exit %d: %s", res.ExitCode, tail(res.Stderr, 500))
	}
	code := res.ExitCode
	if err := e.store.FinishTask(ctx, task.ID, status, &code, errMsg); err != nil {
		return e.outcome(ctx, task.ID), err
	}
	return e.outcome(ctx, task.ID), nil
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
			if err := e.store.ReviewObservation(ctx, o.ID, model.ReviewConfirmed); err != nil {
				continue
			}
			f, err := e.store.CreateFinding(ctx, store.NewFinding{
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
			inv, err := e.store.CreateInvestigation(ctx, model.Investigation{
				ProjectID: projectID, ApplicationID: appID, ObservationID: o.ID, Title: o.Title,
			})
			if err == nil {
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
		overrides, _ := e.store.ListDispositionRules(ctx, projectID)
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
		if app, err := e.store.GetApplication(ctx, *appID); err == nil {
			return app.ProjectID
		}
	}
	return ""
}

// correlateReachability ties a CVE observation to the project's shared reachability verdicts (ADR-0031).
// If the observation carries its own `reachable` attribute (from a reachability analyzer like govulncheck),
// its verdict is recorded for other tools to reuse. Otherwise a CVE finding from a tool without reachability
// (e.g. grype) inherits any stored verdict, so its routing can gate on reachability. Best-effort.
func (e *Engine) correlateReachability(ctx context.Context, projectID string, o *model.Observation) {
	if !strings.HasPrefix(o.RuleID, "CVE-") { // reachability is correlated by CVE id
		return
	}
	cve := o.RuleID
	if own := o.Attributes["reachable"]; own != "" {
		_ = e.store.SetReachability(ctx, projectID, cve, o.Attributes["package"], own == "true", o.Attributes["tool"])
		return
	}
	if reachable, known := e.store.ReachabilityForCVE(ctx, projectID, cve); known {
		if o.Attributes == nil {
			o.Attributes = map[string]string{}
		}
		o.Attributes["reachable"] = strconv.FormatBool(reachable)
	}
}

// correlateExposedRoute ties a source finding to a specific exposed HTTP entry point (ADR-0033): when the
// finding's location file declares a route, and that route is exposed (traffic-confirmed, or the service is
// exposed at all), it sets `exposed_route` ("METHOD /path") and `route_observed`. This refines the coarse
// project-level `exposed` to a concrete route. File-level proximity — the finding sits in the handler file,
// not proven reachable from the route by a call graph. Best-effort; a nil route inventory just skips it.
func (e *Engine) correlateExposedRoute(ctx context.Context, projectID, exposedAttr string, o *model.Observation) {
	file := locationFile(o.Location)
	if file == "" {
		return
	}
	routes, err := e.store.RoutesForHandlerFile(ctx, projectID, file)
	if err != nil || len(routes) == 0 {
		return
	}
	// Prefer a traffic-confirmed route (RoutesForHandlerFile orders observed first). Otherwise the route is
	// only exposed if the service is exposed at all.
	r := routes[0]
	if !r.Observed && exposedAttr != "true" {
		return
	}
	if o.Attributes == nil {
		o.Attributes = map[string]string{}
	}
	o.Attributes["exposed_route"] = strings.TrimSpace(r.Method + " " + r.Path)
	o.Attributes["route_observed"] = strconv.FormatBool(r.Observed)
}

// locationFile returns the file part of an observation location ("file:line" → "file"), stripping only a
// trailing :<digits> line number. A bare path, or an nmap "host:port/proto", is returned unchanged (and
// simply won't match a route's handler_file).
func locationFile(loc string) string {
	i := strings.LastIndex(loc, ":")
	if i <= 0 {
		return loc // no colon (or a leading one) — treat the whole value as the file
	}
	suffix := loc[i+1:]
	if suffix == "" {
		return loc
	}
	for _, c := range suffix {
		if c < '0' || c > '9' {
			return loc // not a pure line number (e.g. host:port/proto)
		}
	}
	return loc[:i]
}

func (e *Engine) auditDisposition(ctx context.Context, action, target string, o model.Observation) {
	data, _ := json.Marshal(map[string]string{"observation": o.ID, "rule": o.RuleID})
	_, _ = e.store.AppendAudit(ctx, "disposition", action, target, data)
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
		if app, err := e.store.GetApplication(ctx, *applicationID); err == nil {
			projectID = app.ProjectID
		}
	}
	if projectID == "" {
		return nil
	}
	entries, err := e.store.ListScopeEntries(ctx, projectID)
	if err != nil {
		return fmt.Errorf("load scope: %w", err)
	}
	if len(entries) == 0 {
		return nil
	}
	target, _ := req.Params[targetParam].(string)
	rules := make([]scope.Entry, len(entries))
	for i, en := range entries {
		rules[i] = scope.Entry{Kind: en.Kind, Value: en.Value}
	}
	if err := scope.Check(rules, target); err != nil {
		return fmt.Errorf("%w: %v", ErrOutOfScope, err)
	}
	return nil
}

func (e *Engine) outcome(ctx context.Context, taskID string) Outcome {
	t, _ := e.store.GetTask(ctx, taskID)
	a, _ := e.store.ListArtifactsByTask(ctx, taskID)
	o, _ := e.store.ListObservationsByTask(ctx, taskID)
	return Outcome{Task: t, Artifacts: a, Observations: o}
}

func tail(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}
