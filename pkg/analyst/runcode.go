package analyst

import (
	"context"
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// run_code (ADR-0020) lets an agent run a command in a sandbox with the project workspace mounted — to
// build and run a real test case or PoC over files it staged there. It refines "no host shell" into a
// sandboxed, gated exec surface: it has network access (a PoC must be able to reach a target, install a
// tool, etc.) and is resource/time-limited. The control is the approval gate — every run_code is gated,
// so a human sees and authorizes the command before it runs.
const (
	defaultRunboxImage = "alpine:3"
	runCodeTimeout     = 120 * time.Second
)

func runCode(ctx context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	root, err := projectWorkspace(deps, "run_code")
	if err != nil {
		return "", err
	}
	if deps.Runner == nil {
		return "", errors.New("run_code: sandbox runner unavailable")
	}
	command := stringArg(call, "command")
	if command == "" {
		return "", errors.New("run_code requires 'command'")
	}
	image := stringArg(call, "image")
	if image == "" {
		image = projectRuntimeImage(ctx, deps)
	}

	timeout := runCodeTimeout
	memoryMB := 512
	if image != defaultRunboxImage {
		timeout = 10 * time.Minute
		memoryMB = 2048
	}

	res, err := deps.Runner.Run(ctx, runner.RunSpec{
		Image:    image,
		Cmd:      []string{"sh", "-c", command},
		Mounts:   []runner.Mount{{Source: root, Target: "/work", ReadOnly: false}},
		Workdir:  "/work",
		Network:  "bridge",
		Timeout:  timeout,
		MemoryMB: memoryMB,
		CPUs:     1,
	})
	if err != nil {
		return "", err
	}
	return jsonify(map[string]any{
		"exit_code": res.ExitCode,
		"stdout":    truncate(string(res.Stdout), 4000),
		"stderr":    truncate(string(res.Stderr), 2000),
	}, nil)
}

func projectRuntimeImage(ctx context.Context, deps ExecDeps) string {
	if deps.ProjectID != "" && deps.Mgr != nil {
		if pdb, err := deps.Mgr.Project(deps.ProjectID); err == nil && pdb != nil {
			if eng, err := pdb.GetEngagement(ctx, deps.ProjectID); err == nil && eng.RuntimeImage != "" {
				return eng.RuntimeImage
			}
		}
	}
	if deps.Mgr != nil {
		if v, err := deps.Mgr.Global().GetSetting(ctx, "runtime.session_image"); err == nil && v != "" {
			return v
		}
	}
	return defaultRunboxImage
}

var _ = store.ErrNotFound // keep import used
