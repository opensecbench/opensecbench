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
	Params        map[string]any
}

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
	if man.OutputMediaType == interpret.SARIFMediaType {
		if obs, ierr := interpret.SARIF(res.Stdout); ierr == nil {
			for _, o := range obs {
				o.TaskID = &task.ID
				o.ArtifactID = &art.ID
				_, _ = e.store.CreateObservation(ctx, o)
			}
		}
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
