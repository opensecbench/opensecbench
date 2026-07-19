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
	"sync"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/interpret"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/scope"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// ErrTaskNotRunning is returned when cancelling a task that is not currently executing.
var ErrTaskNotRunning = errors.New("task: not running")

// Engine executes capabilities and persists their provenance. Runs are asynchronous: Enqueue creates a
// pending task and hands it to a bounded worker pool (ADR-0022) so callers never block on a container.
type Engine struct {
	store    *store.DB
	blobs    *cas.Store
	registry *capability.Registry
	runner   runner.Runner

	// Secrets, if set, resolves a vault secret name to its plaintext for exec-time injection
	// (ADR-0011). nil disables secret injection.
	Secrets func(ctx context.Context, name string) (string, error)

	jobs       chan job          // enqueued work drained by the worker pool
	baseCtx    context.Context   // parent context for background runs (survives request cancellation)
	baseCancel context.CancelFunc
	wg         sync.WaitGroup    // worker goroutines

	mu        sync.Mutex
	running   map[string]runState
	cancelled map[string]bool // tasks cancelled while still queued (checked before a worker starts them)
}

type runState struct {
	cancel    context.CancelFunc
	container string
}

// job is one enqueued capability run: a created (pending) task plus everything needed to execute it.
type job struct {
	task model.Task
	req  RunRequest
	prep prepared
}

// defaultWorkers bounds concurrent capability runs; OSB_TASK_WORKERS overrides. A burst of scheduled or
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

// NewEngine wires the engine's dependencies and starts the async worker pool. Any tasks left pending or
// running from a previous process (a crash mid-run) are reconciled to failed on startup.
func NewEngine(st *store.DB, blobs *cas.Store, reg *capability.Registry, r runner.Runner) *Engine {
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		store: st, blobs: blobs, registry: reg, runner: r,
		jobs:       make(chan job, 256),
		baseCtx:    ctx,
		baseCancel: cancel,
		running:    make(map[string]runState),
		cancelled:  make(map[string]bool),
	}
	if st != nil {
		if n, err := st.FailUnfinishedTasks(ctx, "interrupted (control plane restarted)"); err == nil && n > 0 {
			log.Printf("task: reconciled %d unfinished task(s) to failed on startup", n)
		}
	}
	for i := 0; i < workerCount(); i++ {
		e.wg.Add(1)
		go e.worker()
	}
	return e
}

// Close stops the worker pool and cancels any in-flight runs. Safe to call once.
func (e *Engine) Close() {
	e.baseCancel()
	close(e.jobs)
	e.wg.Wait()
}

// worker drains the queue, running each job on the engine's background context so a client disconnect
// doesn't abort the run. A task cancelled while still queued is skipped and recorded as cancelled.
func (e *Engine) worker() {
	defer e.wg.Done()
	for j := range e.jobs {
		if e.claim(j.task.ID) {
			continue // cancelled before it started
		}
		if err := e.store.StartTask(e.baseCtx, j.task.ID); err != nil {
			continue // already cancelled/gone
		}
		j.task.Status = model.TaskRunning
		_, _ = e.execute(e.baseCtx, j.task, j.req, j.prep)
	}
}

// claim returns true if the task was cancelled while queued (and clears the marker); the worker then
// skips it. False means it is clear to run.
func (e *Engine) claim(taskID string) (cancelledBeforeStart bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancelled[taskID] {
		delete(e.cancelled, taskID)
		return true
	}
	return false
}

// Cancel stops a task. A running task's container is killed and its context cancelled; a still-queued
// task is marked so the worker skips it, and recorded as cancelled now.
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
	// Not executing — it may be sitting in the queue. If the task is still pending, mark it for the
	// worker to skip and record the cancellation immediately.
	t, err := e.store.GetTask(context.Background(), taskID)
	if err != nil || t.Status != model.TaskPending {
		return ErrTaskNotRunning
	}
	e.mu.Lock()
	e.cancelled[taskID] = true
	e.mu.Unlock()
	return e.store.FinishTask(context.Background(), taskID, model.TaskFailed, nil, "cancelled by user")
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
		Queued:            queued,
	})
}

// Enqueue validates and plans the run, records a pending task, and hands it to the worker pool,
// returning immediately. The task advances to running when a worker claims it (ADR-0022). Bad requests
// fail fast (no task); a full queue is the only thing that blocks, and only briefly.
func (e *Engine) Enqueue(ctx context.Context, req RunRequest) (model.Task, error) {
	p, err := e.prepare(ctx, req)
	if err != nil {
		return model.Task{}, err
	}
	t, err := e.createTask(ctx, req, p, true)
	if err != nil {
		return model.Task{}, err
	}
	select {
	case e.jobs <- job{task: t, req: req, prep: p}:
		return t, nil
	case <-e.baseCtx.Done():
		_ = e.store.FinishTask(context.Background(), t.ID, model.TaskFailed, nil, "engine shutting down")
		return t, errors.New("task: engine shutting down")
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

	// Resolve secret references and inject them at exec time — never persisted, never logged; the
	// returned redactor scrubs their values from captured output (ADR-0011).
	redact, injErr := e.injectSecrets(ctx, req, &spec)
	if injErr != nil {
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, injErr.Error())
		return e.outcome(ctx, task.ID), injErr
	}

	// Name the container and register the run so Cancel can stop it.
	spec.Name = "osb-" + task.ID
	runCtx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	e.running[task.ID] = runState{cancel: cancel, container: spec.Name}
	e.mu.Unlock()

	res, runErr := e.runner.Run(runCtx, spec)

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
	}
	for _, o := range interpreted {
		o.TaskID = &task.ID
		o.ArtifactID = &art.ID
		_, _ = e.store.CreateObservation(ctx, o)
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
