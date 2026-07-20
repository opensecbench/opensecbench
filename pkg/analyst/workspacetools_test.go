package analyst

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestWorkspaceWriteReadList(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	root := t.TempDir()
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID, WorkspaceRoot: root})

	// Write into a nested convention dir (parents created automatically).
	if _, err := exec(ctx, agent.ToolCall{Tool: "workspace_write", Args: map[string]any{"path": "reports/draft.md", "content": "# Findings\nSQLi in login."}}); err != nil {
		t.Fatal(err)
	}
	// It lands under <root>/<project>/reports/draft.md and nowhere else.
	if _, err := os.Stat(filepath.Join(root, projectID, "reports", "draft.md")); err != nil {
		t.Fatalf("file not written to the project workspace: %v", err)
	}

	out, err := exec(ctx, agent.ToolCall{Tool: "workspace_read", Args: map[string]any{"path": "reports/draft.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "SQLi in login") {
		t.Fatalf("workspace_read = %s", out)
	}

	out, err = exec(ctx, agent.ToolCall{Tool: "workspace_list", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"name":"reports","type":"dir"`) {
		t.Fatalf("workspace_list root = %s", out)
	}
}

func TestWorkspacePathTraversalBlocked(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID, WorkspaceRoot: t.TempDir()})

	for _, p := range []string{"../escape.txt", "../../etc/cron", "sub/../../out"} {
		if _, err := exec(ctx, agent.ToolCall{Tool: "workspace_write", Args: map[string]any{"path": p, "content": "x"}}); err == nil {
			t.Fatalf("write to %q should be refused as outside the workspace", p)
		}
	}
}

func TestWorkspaceIsolatedPerProject(t *testing.T) {
	ctx := context.Background()
	db, projA := seedProject(t)
	projB, _ := db.CreateProject(ctx, store.NewProject{Name: "B"})
	root := t.TempDir()

	execA := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projA, WorkspaceRoot: root})
	execB := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projB.ID, WorkspaceRoot: root})

	if _, err := execA(ctx, agent.ToolCall{Tool: "workspace_write", Args: map[string]any{"path": "secret.txt", "content": "A's data"}}); err != nil {
		t.Fatal(err)
	}
	// Project B shares the same root but a different subtree — it cannot see A's file.
	if _, err := execB(ctx, agent.ToolCall{Tool: "workspace_read", Args: map[string]any{"path": "secret.txt"}}); err == nil {
		t.Fatal("project B must not read project A's workspace file")
	}
}

func TestWorkspaceNotConfigured(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID}) // no WorkspaceRoot

	if _, err := exec(ctx, agent.ToolCall{Tool: "workspace_write", Args: map[string]any{"path": "a.txt", "content": "x"}}); err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("workspace_write without a configured root should error, got %v", err)
	}
}

func TestWorkspaceNeedsProject(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), WorkspaceRoot: t.TempDir()}) // no project

	if _, err := exec(ctx, agent.ToolCall{Tool: "workspace_write", Args: map[string]any{"path": "a.txt", "content": "x"}}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("workspace_write without a project should error about the project, got %v", err)
	}
}
