package analyst

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"github.com/opensecbench/opensecbench/pkg/agent"
)

// Workspace tools (ADR-0020). A durable, per-project scratch space agents use to coordinate — one agent
// writes a draft or a PoC, another reads it. Confined to <workspaceRoot>/<projectId> (never the host FS,
// no traversal). Distinct from evidence reads (read_file/read_context, which are read-only) and from the
// knowledge base. Standard layout so agents know where to look:
//
//	inventory/  recon/  analysis/  findings/<id>/  reports/  scratch/<run>/
const workspaceDirPerm = 0o755

// projectWorkspace returns (and creates) the current project's workspace root.
func projectWorkspace(deps ExecDeps, tool string) (string, error) {
	projectID, err := requireProject(deps, tool)
	if err != nil {
		return "", err
	}
	if deps.WorkspaceRoot == "" {
		return "", errors.New(tool + ": workspace is not configured")
	}
	root := filepath.Join(deps.WorkspaceRoot, projectID)
	if err := os.MkdirAll(root, workspaceDirPerm); err != nil {
		return "", err
	}
	return root, nil
}

// workspaceWrite writes (or overwrites) a file in the project workspace, creating parent dirs.
func workspaceWrite(_ context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	root, err := projectWorkspace(deps, "workspace_write")
	if err != nil {
		return "", err
	}
	rel := stringArg(call, "path")
	if rel == "" {
		return "", errors.New("workspace_write requires 'path'")
	}
	full, err := confinedPath(root, rel)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(full), workspaceDirPerm); err != nil {
		return "", err
	}
	content := stringArg(call, "content")
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		return "", err
	}
	return jsonify(map[string]any{"path": rel, "bytes": len(content)}, nil)
}

// workspaceRead returns a workspace file's contents (size-capped).
func workspaceRead(_ context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	root, err := projectWorkspace(deps, "workspace_read")
	if err != nil {
		return "", err
	}
	rel := stringArg(call, "path")
	if rel == "" {
		return "", errors.New("workspace_read requires 'path'")
	}
	full, err := confinedPath(root, rel)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	truncated := false
	if len(data) > maxReadBytes {
		data = data[:maxReadBytes]
		truncated = true
	}
	return jsonify(map[string]any{"path": rel, "content": string(data), "truncated": truncated}, nil)
}

// workspaceList lists a workspace directory (root when no path is given).
func workspaceList(_ context.Context, deps ExecDeps, call agent.ToolCall) (string, error) {
	root, err := projectWorkspace(deps, "workspace_list")
	if err != nil {
		return "", err
	}
	full, err := confinedPath(root, stringArg(call, "path"))
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		if os.IsNotExist(err) {
			return jsonify([]any{}, nil) // an unwritten dir is simply empty
		}
		return "", err
	}
	type entry struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Size int64  `json:"size,omitempty"`
	}
	out := make([]entry, 0, len(entries))
	for _, e := range entries {
		kind := "file"
		var size int64
		if e.IsDir() {
			kind = "dir"
		} else if info, err := e.Info(); err == nil {
			size = info.Size()
		}
		out = append(out, entry{Name: e.Name(), Type: kind, Size: size})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "dir"
		}
		return out[i].Name < out[j].Name
	})
	return jsonify(out, nil)
}
