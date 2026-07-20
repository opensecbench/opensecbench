package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// captureRunner records the RunSpec and returns canned output, so run_code's wiring can be tested
// without Docker.
type captureRunner struct{ got runner.RunSpec }

func (c *captureRunner) Name() string { return "capture" }
func (c *captureRunner) Run(_ context.Context, spec runner.RunSpec) (runner.Result, error) {
	c.got = spec
	return runner.Result{ExitCode: 0, Stdout: []byte("hello from sandbox")}, nil
}

func TestRunCode(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	root := t.TempDir()
	cr := &captureRunner{}
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID, WorkspaceRoot: root, Runner: cr})

	out, err := exec(ctx, agent.ToolCall{Tool: "run_code", Args: map[string]any{"command": "echo hi > out.txt && cat out.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello from sandbox") || !strings.Contains(out, `"exit_code":0`) {
		t.Fatalf("run_code result = %s", out)
	}

	spec := cr.got
	if spec.Image != "alpine:3" {
		t.Fatalf("default image = %q", spec.Image)
	}
	if len(spec.Cmd) != 3 || spec.Cmd[0] != "sh" || spec.Cmd[1] != "-c" || !strings.Contains(spec.Cmd[2], "echo hi") {
		t.Fatalf("cmd = %v", spec.Cmd)
	}
	if spec.Network != "bridge" {
		t.Fatalf("run_code should have network access, got %q", spec.Network)
	}
	if len(spec.Mounts) != 1 || spec.Mounts[0].ReadOnly || spec.Mounts[0].Target != "/work" || spec.Workdir != "/work" {
		t.Fatalf("workspace must be mounted read-write at /work: %+v (workdir %q)", spec.Mounts, spec.Workdir)
	}
	// The mount source is the project's workspace subtree.
	if !strings.HasPrefix(spec.Mounts[0].Source, root) || !strings.Contains(spec.Mounts[0].Source, projectID) {
		t.Fatalf("mount source = %q, want the project workspace under %q", spec.Mounts[0].Source, root)
	}
}

func TestRunCodeIsGated(t *testing.T) {
	ctx := context.Background()
	// run_code is arbitrary execution — it must require authorization.
	if ok, _ := Approver(nil)(ctx, agent.ToolCall{Tool: "run_code"}); ok {
		t.Fatal("run_code must be denied without authorization")
	}
	if ok, _ := Approver([]string{"run_code"})(ctx, agent.ToolCall{Tool: "run_code"}); !ok {
		t.Fatal("run_code should be approved when authorized")
	}
}

func TestRunCodeNeedsWorkspace(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID, Runner: &captureRunner{}}) // no WorkspaceRoot

	if _, err := exec(ctx, agent.ToolCall{Tool: "run_code", Args: map[string]any{"command": "echo hi"}}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("run_code without a workspace should error, got %v", err)
	}
}
