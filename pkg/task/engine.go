// Package task runs capabilities and records their provenance. The engine ties together the
// capability registry, a sandboxed runner (ADR-0004), the content-addressed store, and the
// database so that every run produces a task and immutable output artifacts with full lineage.
package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/interpret"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// Engine executes capabilities and persists their provenance.
type Engine struct {
	store    *store.DB
	blobs    *cas.Store
	registry *capability.Registry
	runner   runner.Runner
}

// NewEngine wires the engine's dependencies.
func NewEngine(st *store.DB, blobs *cas.Store, reg *capability.Registry, r runner.Runner) *Engine {
	return &Engine{store: st, blobs: blobs, registry: reg, runner: r}
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

	spec, err := c.Plan(capability.Input{TargetDir: req.TargetDir, Params: req.Params})
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
		ApplicationID:     req.ApplicationID,
		AssetID:           req.AssetID,
		Actor:             actor,
		Runner:            e.runner.Name(),
		Params:            paramsJSON,
	})
	if err != nil {
		return Outcome{}, err
	}

	res, runErr := e.runner.Run(ctx, spec)
	if runErr != nil {
		_ = e.store.FinishTask(ctx, task.ID, model.TaskFailed, nil, runErr.Error())
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
