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
	"os/exec"
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

// Engine executes capabilities and persists their provenance.
type Engine struct {
	store    *store.DB
	blobs    *cas.Store
	registry *capability.Registry
	runner   runner.Runner

	// Secrets, if set, resolves a vault secret name to its plaintext for exec-time injection
	// (ADR-0011). nil disables secret injection.
	Secrets func(ctx context.Context, name string) (string, error)

	mu      sync.Mutex
	running map[string]runState
}

type runState struct {
	cancel    context.CancelFunc
	container string
}

// NewEngine wires the engine's dependencies.
func NewEngine(st *store.DB, blobs *cas.Store, reg *capability.Registry, r runner.Runner) *Engine {
	return &Engine{store: st, blobs: blobs, registry: reg, runner: r, running: make(map[string]runState)}
}

// Cancel stops a running task by killing its container and cancelling its context. The in-flight
// Run then records the task as failed.
func (e *Engine) Cancel(taskID string) error {
	e.mu.Lock()
	rs, ok := e.running[taskID]
	e.mu.Unlock()
	if !ok {
		return ErrTaskNotRunning
	}
	if rs.container != "" {
		_ = exec.Command("docker", "kill", rs.container).Run() // best-effort
	}
	rs.cancel()
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
}

// ErrOutOfScope is returned when a network capability's target is not in the project allowlist.
var ErrOutOfScope = errors.New("task: target out of scope")

// Outcome is a completed task with its artifacts and any observations interpreted from them.
type Outcome struct {
	Task         model.Task          `json:"task"`
	Artifacts    []model.Artifact    `json:"artifacts"`
	Observations []model.Observation `json:"observations"`
}

// Run plans the capability, executes it in the sandbox, stores its stdout as an output artifact
// in the CAS, and records the task's outcome. Provenance links artifact → task → capability+
// version → runner.
func (e *Engine) Run(ctx context.Context, req RunRequest) (Outcome, error) {
	c, ok := e.registry.Get(req.CapabilityID)
	if !ok {
		return Outcome{}, fmt.Errorf("unknown capability %q", req.CapabilityID)
	}
	man := c.Manifest()

	// Resolve the target directory from a source-repo asset when not given explicitly, and carry
	// the asset's application onto the task for provenance.
	targetDir := req.TargetDir
	applicationID := req.ApplicationID
	if targetDir == "" && req.AssetID != nil {
		asset, err := e.store.GetAsset(ctx, *req.AssetID)
		if err != nil {
			return Outcome{}, fmt.Errorf("resolve asset: %w", err)
		}
		if asset.Type != model.AssetSourceRepo {
			return Outcome{}, fmt.Errorf("asset %s is %s; only source_repo assets have a target directory", asset.ID, asset.Type)
		}
		targetDir = asset.Location
		if applicationID == nil {
			applicationID = &asset.ApplicationID
		}
	}

	spec, err := c.Plan(capability.Input{TargetDir: targetDir, Params: req.Params})
	if err != nil {
		return Outcome{}, err
	}

	actor := req.Actor
	if actor == "" {
		actor = "human"
	}
	paramsJSON, _ := json.Marshal(req.Params)
	task, err := e.store.CreateTask(ctx, store.NewTask{
		CapabilityID:      man.ID,
		CapabilityVersion: man.Version,
		ApplicationID:     applicationID,
		AssetID:           req.AssetID,
		Actor:             actor,
		Runner:            e.runner.Name(),
		Params:            paramsJSON,
	})
	if err != nil {
		return Outcome{}, err
	}

	// Scope guard: a network capability may only touch in-scope targets (P6). The task record
	// above captures the blocked attempt for the audit trail.
	if man.TargetParam != "" {
		if scopeErr := e.checkScope(ctx, req, applicationID, man.TargetParam); scopeErr != nil {
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
