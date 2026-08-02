package analyst

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// seedSourceAsset creates project → application → source_repo asset over a temp dir with a couple of files.
func seedSourceAsset(t *testing.T, sensitivity string) (db *store.DB, projectID, assetID string) {
	t.Helper()
	db = migratedStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, s string) {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("auth/login.go", "package auth\n\nfunc Login() {\n\t// TODO: insecure default credentials\n}\n")
	write("main.go", "package main\n\nfunc main() {}\n")
	asset, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: dir, Sensitivity: sensitivity})
	if err != nil {
		t.Fatal(err)
	}
	return db, proj.ID, asset.ID
}

func TestReadFile(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "auth/login.go"}})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "func Login") || !strings.Contains(res.Content, "insecure default") {
		t.Fatalf("read_file returned unexpected content: %q", res.Content)
	}
}

func TestReadFilePathTraversalBlocked(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	for _, p := range []string{"../../../etc/passwd", "/etc/passwd", "auth/../../escape"} {
		if _, err := exec(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": p}}); err == nil {
			t.Fatalf("path %q should be refused as outside the asset root", p)
		}
	}
}

func TestGrepCodeAndFindFiles(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "grep_code", Args: map[string]any{"asset": assetID, "pattern": "insecure"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "auth/login.go") || !strings.Contains(out, "\"count\":1") {
		t.Fatalf("grep_code = %s", out)
	}

	out, err = exec(ctx, agent.ToolCall{Tool: "find_files", Args: map[string]any{"asset": assetID, "glob": "*.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "main.go") || !strings.Contains(out, "auth/login.go") {
		t.Fatalf("find_files = %s", out)
	}
}

func TestListDir(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "list_dir", Args: map[string]any{"asset": assetID}})
	if err != nil {
		t.Fatal(err)
	}
	// Directories sort first; auth/ and main.go must both appear.
	if !strings.Contains(out, `"name":"auth","type":"dir"`) || !strings.Contains(out, "main.go") {
		t.Fatalf("list_dir = %s", out)
	}
}

func TestReadFileCrossProjectRefused(t *testing.T) {
	ctx := context.Background()
	db, _, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	// A different project reading the first project's asset must be refused.
	other, _ := db.CreateProject(ctx, store.NewProject{Name: "Other"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: other.ID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "main.go"}}); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("cross-project read should be refused, got %v", err)
	}
}

func TestReadFileRejectsNonSourceAsset(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	doc, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetDocument, Location: "/x", Sensitivity: model.SensitivityOpenSource})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: proj.ID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": doc.ID, "path": "a"}}); err == nil || !strings.Contains(err.Error(), "source_repo") {
		t.Fatalf("read_file on a non-source asset should be refused, got %v", err)
	}
}

func TestCodeToolsNeedProject(t *testing.T) {
	ctx := context.Background()
	db, _, assetID := seedSourceAsset(t, model.SensitivityOpenSource)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db)}) // no project

	if _, err := exec(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "main.go"}}); err == nil || !strings.Contains(err.Error(), "project") {
		t.Fatalf("read_file without a project should error about the project, got %v", err)
	}
}

func TestDLPBlocksPrivateSourceReadOnExternalProvider(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityPrivate)

	svc := &Service{mgr: store.NewCombinedManager(db)}
	// Open-source-cleared destination + external provider: reading a PRIVATE asset's source is blocked.
	if out, err := svc.executeFor(projectID, &llm.AnthropicProvider{}, model.SensitivityOpenSource)(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "main.go"}}); err != nil || !strings.Contains(out, "withheld") {
		t.Fatalf("private source read should be withheld, got out=%s err=%v", out, err)
	}
	// The same read on a LOCAL provider is never egress-blocked.
	if _, err := svc.executeFor(projectID, &llm.MockProvider{}, model.SensitivityOpenSource)(ctx, agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "main.go"}}); err != nil {
		t.Fatalf("local provider read should not be blocked: %v", err)
	}
}

// The middle "internal" tier egresses to an internal-cleared destination but not an open-source-only one.
func TestEgressTierInternal(t *testing.T) {
	ctx := context.Background()
	db, projectID, assetID := seedSourceAsset(t, model.SensitivityInternal)
	read := agent.ToolCall{Tool: "read_file", Args: map[string]any{"asset": assetID, "path": "main.go"}}

	// Internal-cleared destination: private not permitted, but an internal read to an external provider passes.
	corp := &Service{mgr: store.NewCombinedManager(db)}
	if _, err := corp.executeFor(projectID, &llm.AnthropicProvider{}, model.SensitivityInternal)(ctx, read); err != nil {
		t.Fatalf("internal read to an internal-cleared destination should be allowed, got %v", err)
	}

	// Open-source-only destination: internal egress blocked.
	strict := &Service{mgr: store.NewCombinedManager(db)}
	if out, err := strict.executeFor(projectID, &llm.AnthropicProvider{}, model.SensitivityOpenSource)(ctx, read); err != nil || !strings.Contains(out, "withheld") {
		t.Fatalf("internal read to an open-source-only destination should be withheld, got out=%s err=%v", out, err)
	}
}
