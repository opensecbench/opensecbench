package analyst

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	svc := NewService(db, nil, mock) // engine nil: must not be reached (denied)

	th, err := db.CreateThread(ctx, store.NewThread{Title: "t"})
	if err != nil {
		t.Fatal(err)
	}

	res, err := svc.Send(ctx, th.ID, "scan the payments repo")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil || res.Pending.Tool != "run_capability" {
		t.Fatalf("expected a pending approval, got %+v", res)
	}
	if res.Thread.Status != model.ThreadAwaitingApproval {
		t.Fatalf("thread status = %s, want awaiting_approval", res.Thread.Status)
	}

	res2, err := svc.Decide(ctx, res.Pending.ID, "deny")
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
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), runner.LocalRunner{})

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "x.go"), []byte("y"), 0o600)
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})

	mock := &llm.MockProvider{Responses: []string{
		fmt.Sprintf(`{"tool":"run_capability","args":{"capability":"source-inventory","asset":%q}}`, asset.ID),
		`{"answer":"Inventory complete."}`,
	}}
	svc := NewService(db, engine, mock)

	th, _ := db.CreateThread(ctx, store.NewThread{})
	res, err := svc.Send(ctx, th.ID, "inventory it")
	if err != nil {
		t.Fatal(err)
	}
	if res.Pending == nil {
		t.Fatalf("expected pause, got %+v", res)
	}

	res2, err := svc.Decide(ctx, res.Pending.ID, "approve")
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
