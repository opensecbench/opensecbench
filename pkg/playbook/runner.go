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
	store  *store.DB
}

// NewRunner wires the playbook runner.
func NewRunner(engine *task.Engine, st *store.DB) *Runner {
	return &Runner{engine: engine, store: st}
}

// RunResult is a playbook run with its per-step outcomes.
type RunResult struct {
	Run      model.PlaybookRun `json:"run"`
	Outcomes []task.Outcome    `json:"outcomes"`
}

// Run executes a playbook's steps sequentially against an asset. A step that fails to run stops
// the playbook; a step whose task fails (e.g. a scan error) is recorded and the run continues but
// is marked failed.
func (r *Runner) Run(ctx context.Context, playbookID, assetID, actor string) (RunResult, error) {
	pb, ok := Get(playbookID)
	if !ok {
		return RunResult{}, fmt.Errorf("unknown playbook %q", playbookID)
	}

	var assetPtr *string
	if assetID != "" {
		assetPtr = &assetID
	}
	run, err := r.store.CreatePlaybookRun(ctx, playbookID, assetPtr, actor)
	if err != nil {
		return RunResult{}, err
	}

	status := model.PlaybookSucceeded
	var outcomes []task.Outcome
	for i, step := range pb.Steps {
		out, err := r.engine.Run(ctx, task.RunRequest{
			CapabilityID: step.Capability,
			AssetID:      assetPtr,
			Actor:        actor,
			Params:       step.Params,
		})
		if err != nil {
			_ = r.store.FinishPlaybookRun(ctx, run.ID, model.PlaybookFailed)
			return RunResult{Run: r.reload(ctx, run.ID), Outcomes: outcomes}, err
		}
		_ = r.store.AddRunTask(ctx, run.ID, out.Task.ID, i)
		outcomes = append(outcomes, out)
		if out.Task.Status == model.TaskFailed {
			status = model.PlaybookFailed
		}
	}

	_ = r.store.FinishPlaybookRun(ctx, run.ID, status)
	return RunResult{Run: r.reload(ctx, run.ID), Outcomes: outcomes}, nil
}

func (r *Runner) reload(ctx context.Context, id string) model.PlaybookRun {
	pr, _ := r.store.GetPlaybookRun(ctx, id)
	return pr
}
