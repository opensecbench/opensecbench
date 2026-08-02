package analyst

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
	"github.com/opensecbench/opensecbench/pkg/task"
)

func migratedStore(t *testing.T) *store.DB {
	t.Helper()
	db := storetest.New(t)
	return db
}

func TestApproverGatesCapabilityExecution(t *testing.T) {
	ctx := context.Background()

	deny := Approver(nil)
	if ok, _ := deny(ctx, agent.ToolCall{Tool: "list_projects"}); !ok {
		t.Fatal("read-only tool should auto-approve")
	}
	if ok, _ := deny(ctx, agent.ToolCall{Tool: "run_capability"}); ok {
		t.Fatal("run_capability must be denied without authorization")
	}

	allow := Approver([]string{"run_capability"})
	if ok, _ := allow(ctx, agent.ToolCall{Tool: "run_capability"}); !ok {
		t.Fatal("run_capability should be approved when authorized")
	}
}

func TestAnalystDeniesUnauthorizedCapability(t *testing.T) {
	db := migratedStore(t)
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"run_capability","args":{"capability":"source-inventory","asset":"x"}}`,
		`{"answer":"I need authorization to run that."}`,
	}}
	// engine is nil: it must never be reached because the tool is denied.
	loop := NewLoop(mock, store.NewCombinedManager(db), nil, nil, nil)

	res, err := loop.Run(context.Background(), "scan it")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 1 || res.Steps[0].Approved || res.Steps[0].Result != "(denied)" {
		t.Fatalf("expected a single denied step, got %+v", res.Steps)
	}
}

func TestAnalystRunsCapabilityWhenAuthorized(t *testing.T) {
	if !runner.Available() {
		t.Skip("docker not available")
	}
	db := migratedStore(t)
	ctx := context.Background()

	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), runner.LocalRunner{})

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})
	if err != nil {
		t.Fatal(err)
	}

	mock := &llm.MockProvider{Responses: []string{
		fmt.Sprintf(`{"tool":"run_capability","args":{"capability":"source-inventory","asset":%q}}`, asset.ID),
		`{"answer":"Ran the inventory."}`,
	}}
	var audited []string
	loop := NewLoop(mock, store.NewCombinedManager(db), engine, []string{"run_capability"}, func(action, _ string) { audited = append(audited, action) })

	res, err := loop.Run(ctx, "inventory the repo")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 1 || !res.Steps[0].Approved {
		t.Fatalf("expected an approved step, got %+v", res.Steps)
	}
	if res.Steps[0].Error != "" || !strings.Contains(res.Steps[0].Result, "succeeded") {
		t.Fatalf("capability did not run cleanly: %+v", res.Steps[0])
	}
	if !containsStr(audited, "agent.tool.executed") {
		t.Fatalf("missing audit event: %v", audited)
	}
}

func containsStr(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
