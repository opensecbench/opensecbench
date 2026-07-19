package analyst

import (
	"context"
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/runner"
)

// run_code (ADR-0020) lets an agent run a command in a sandbox with the project workspace mounted — to
// build and run a test case or PoC over files it staged there. It refines "no host shell" into a
// sandboxed, gated exec surface: gated (arbitrary execution needs approval), no network (a PoC that must
// reach a target uses send_request, which is scope-guarded), resource/time-limited.
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
		image = defaultRunboxImage
	}

	res, err := deps.Runner.Run(ctx, runner.RunSpec{
		Image:    image,
		Cmd:      []string{"sh", "-c", command},
		Mounts:   []runner.Mount{{Source: root, Target: "/work", ReadOnly: false}},
		Workdir:  "/work",
		Network:  "none",
		Timeout:  runCodeTimeout,
		MemoryMB: 512,
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
