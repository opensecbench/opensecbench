package playbook

import (
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// Runner executes playbooks by running each step's capability via the task engine.
type Runner struct {
	engine *task.Engine
	mgr    *store.Manager
}

// NewRunner wires the playbook runner.
func NewRunner(engine *task.Engine, mgr *store.Manager) *Runner {
	return &Runner{engine: engine, mgr: mgr}
}

// p is the per-project database a playbook run and its rows live in (ADR-0049), falling back to the
// global handle so a nil resolution never panics.
func (r *Runner) p(projectID string) *store.DB {
	if r.mgr == nil {
		return nil
	}
	db, err := r.mgr.Project(projectID)
	if err != nil || db == nil {
		return r.mgr.Global()
	}
	return db
}

// RunResult is a playbook run with its per-step outcomes.
type RunResult struct {
	Run      model.PlaybookRun `json:"run"`
	Outcomes []task.Outcome    `json:"outcomes"`
}

// Start creates a playbook run and executes its steps in the background, returning the run immediately
// (ADR-0022). The client polls GET /v1/playbook-runs/{id} for progress. A bad playbook fails fast with
// no run created. projectID scopes the run to a project's database and its capability tasks.
func (r *Runner) Start(ctx context.Context, projectID, playbookID, assetID, actor string) (model.PlaybookRun, error) {
	pb, ok := Get(playbookID)
	if !ok {
		return model.PlaybookRun{}, fmt.Errorf("unknown playbook %q", playbookID)
	}
	assetPtr := assetPointer(assetID)
	run, err := r.p(projectID).CreatePlaybookRun(ctx, playbookID, assetPtr, actor)
	if err != nil {
		return model.PlaybookRun{}, err
	}
	// Execute off the request context so the run survives client disconnect; the per-step capability
	// tasks are themselves tracked (and reconciled on restart) by the engine.
	go func() { _, _ = r.execute(context.Background(), projectID, run.ID, pb, assetPtr, actor) }()
	return run, nil
}

// Run executes a playbook's steps sequentially against an asset and blocks until done, returning the
// per-step outcomes. Retained for callers that need in-line completion (tests); the HTTP path uses Start.
func (r *Runner) Run(ctx context.Context, projectID, playbookID, assetID, actor string) (RunResult, error) {
	pb, ok := Get(playbookID)
	if !ok {
		return RunResult{}, fmt.Errorf("unknown playbook %q", playbookID)
	}
	assetPtr := assetPointer(assetID)
	run, err := r.p(projectID).CreatePlaybookRun(ctx, playbookID, assetPtr, actor)
	if err != nil {
		return RunResult{}, err
	}
	outcomes, runErr := r.execute(ctx, projectID, run.ID, pb, assetPtr, actor)
	return RunResult{Run: r.reload(ctx, projectID, run.ID), Outcomes: outcomes}, runErr
}

// execute runs each step's capability in order. A step that fails to run stops the playbook; a step
// whose task fails (e.g. a scan error) is recorded and the run continues but is marked failed.
func (r *Runner) execute(ctx context.Context, projectID, runID string, pb Playbook, assetPtr *string, actor string) ([]task.Outcome, error) {
	status := model.PlaybookSucceeded
	var outcomes []task.Outcome
	pid := &projectID
	for i, step := range pb.Steps {
		out, err := r.engine.Run(ctx, task.RunRequest{
			CapabilityID: step.Capability,
			AssetID:      assetPtr,
			ProjectID:    pid,
			Actor:        actor,
			Params:       step.Params,
		})
		if err != nil {
			_ = r.p(projectID).FinishPlaybookRun(ctx, runID, model.PlaybookFailed)
			return outcomes, err
		}
		_ = r.p(projectID).AddRunTask(ctx, runID, out.Task.ID, i)
		outcomes = append(outcomes, out)
		if out.Task.Status == model.TaskFailed {
			status = model.PlaybookFailed
		}
	}
	_ = r.p(projectID).FinishPlaybookRun(ctx, runID, status)
	return outcomes, nil
}

func assetPointer(assetID string) *string {
	if assetID == "" {
		return nil
	}
	return &assetID
}

func (r *Runner) reload(ctx context.Context, projectID, id string) model.PlaybookRun {
	pr, _ := r.p(projectID).GetPlaybookRun(ctx, id)
	return pr
}
