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
	"github.com/opensecbench/opensecbench/pkg/task"
)

func TestServiceGatedToolPausesThenDenies(t *testing.T) {
	db := migratedStore(t)
	ctx := context.Background()

	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"run_capability","args":{"capability":"semgrep","asset":"x"}}`, // gated -> pause
		`{"answer":"Understood, I won't run it."}`,
	}}
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", mock) // engine nil: must not be reached (denied)

	th, err := db.CreateThread(ctx, store.NewThread{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Send(ctx, projectOf(th), th.ID, "scan the payments repo")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil || res.Pending.Tool != "run_capability" {
		t.Fatalf("expected a pending approval, got %+v", res)
	}
	if res.Thread.Status != model.ThreadAwaitingApproval {
		t.Fatalf("thread status = %s, want awaiting_approval", res.Thread.Status)
	}

	res2, err := svc.Decide(ctx, "", res.Pending.ID, "deny")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Answer == "" || res2.Thread.Status != model.ThreadActive {
		t.Fatalf("deny did not resume to an answer: %+v", res2)
	}
	if res2.Pending != nil {
		t.Fatal("no new approval expected after denial")
	}
}

func TestServiceApproveRunsCapability(t *testing.T) {
	if !runner.Available() {
		t.Skip("docker not available")
	}
	db := migratedStore(t)
	ctx := context.Background()

	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	engine := task.NewEngine(store.NewCombinedManager(db), blobs, capability.BuiltIns(), runner.LocalRunner{})

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "x.go"), []byte("y"), 0o600)
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})

	mock := &llm.MockProvider{Responses: []string{
		fmt.Sprintf(`{"tool":"run_capability","args":{"capability":"source-inventory","asset":%q}}`, asset.ID),
		`{"answer":"Inventory complete."}`,
	}}
	svc := NewService(store.NewCombinedManager(db), engine, nil, "", mock)

	th, _ := db.CreateThread(ctx, store.NewThread{})
	res, err := svc.Send(ctx, projectOf(th), th.ID, "inventory it")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil {
		t.Fatalf("expected pause, got %+v", res)
	}

	res2, err := svc.Decide(ctx, "", res.Pending.ID, "approve")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Answer == "" {
		t.Fatalf("approve did not complete: %+v", res2)
	}
	// A tool-result message should confirm the capability ran.
	ran := false
	for _, m := range res2.NewMessages {
		if strings.Contains(m.Content, "succeeded") {
			ran = true
		}
	}
	if !ran {
		t.Fatalf("capability result not in transcript: %+v", res2.NewMessages)
	}
}

func TestEgressPolicyBlocksPrivateAssetOnExternalProvider(t *testing.T) {
	db := migratedStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	priv, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/work/private", Sensitivity: model.SensitivityPrivate})
	oss, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/oss/pub", Sensitivity: model.SensitivityOpenSource})

	svc := &Service{mgr: store.NewCombinedManager(db), egressStrict: true}
	ext := &llm.AnthropicProvider{} // IsLocal → false (external)
	local := &llm.MockProvider{}    // IsLocal → true

	// Strict egress + external provider: running a capability on a PRIVATE asset is blocked.
	_, err := svc.executeFor("", ext)(ctx, agent.ToolCall{Tool: "run_capability", Args: map[string]any{"capability": "semgrep", "asset": priv.ID}})
	if err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("expected egress block for private asset, got %v", err)
	}
	// An open-source asset is not blocked by egress (it fails later for a different reason).
	_, err = svc.executeFor("", ext)(ctx, agent.ToolCall{Tool: "run_capability", Args: map[string]any{"capability": "semgrep", "asset": oss.ID}})
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatal("open-source asset must not be egress-blocked")
	}
	// The same private asset on a LOCAL provider is never egress-blocked.
	_, err = svc.executeFor("", local)(ctx, agent.ToolCall{Tool: "run_capability", Args: map[string]any{"capability": "semgrep", "asset": priv.ID}})
	if err != nil && strings.Contains(err.Error(), "egress") {
		t.Fatal("local provider must not be egress-blocked")
	}
}
