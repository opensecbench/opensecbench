package playbook

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// fakePBRunner returns canned stdout without Docker, so async playbook execution can be exercised.
type fakePBRunner struct{}

func (fakePBRunner) Name() string { return "fake" }
func (fakePBRunner) Run(context.Context, runner.RunSpec) (runner.Result, error) {
	return runner.Result{Stdout: []byte("cmd/main.go\n"), ExitCode: 0}, nil
}

func TestRunnerStartAsync(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakePBRunner{})
	defer engine.Close()

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "x.go"), []byte("y"), 0o600)
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})

	// Start returns immediately with a running run; execution happens in the background.
	run, err := NewRunner(engine, store.NewCombinedManager(db)).Start(ctx, proj.ID, "recon", asset.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != model.PlaybookRunning {
		t.Fatalf("started run status = %s, want running", run.Status)
	}

	// Poll to completion.
	var final model.PlaybookRun
	for i := 0; i < 300; i++ {
		final, _ = db.GetPlaybookRun(ctx, run.ID)
		if final.Status != model.PlaybookRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if final.Status != model.PlaybookSucceeded {
		t.Fatalf("async playbook run status = %s, want succeeded", final.Status)
	}
	if len(final.TaskIDs) == 0 {
		t.Fatal("async run recorded no step tasks")
	}
}

func TestRunnerStartUnknownPlaybook(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakePBRunner{})
	defer engine.Close()

	if _, err := NewRunner(engine, store.NewCombinedManager(db)).Start(context.Background(), "", "does-not-exist", "", "human"); err == nil {
		t.Fatal("expected an error for an unknown playbook")
	}
	if runs, _ := db.ListPlaybookRuns(context.Background(), 10); len(runs) != 0 {
		t.Fatalf("a bad Start should create no run, got %d", len(runs))
	}
}

func TestBuiltInsAndGet(t *testing.T) {
	if len(BuiltIns()) == 0 {
		t.Fatal("no built-in playbooks")
	}
	p, ok := Get("source-review")
	if !ok || len(p.Steps) != 2 {
		t.Fatalf("source-review = %+v, ok=%v", p, ok)
	}
	if _, ok := Get("nope"); ok {
		t.Fatal("unknown playbook returned ok")
	}
}

func TestRunnerRunsPlaybook(t *testing.T) {
	if !runner.Available() {
		t.Skip("docker not available")
	}
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), runner.LocalRunner{})

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "x.go"), []byte("y"), 0o600)
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})

	res, err := NewRunner(engine, store.NewCombinedManager(db)).Run(ctx, proj.ID, "recon", asset.ID, "human")
	if err != nil {
		t.Fatal(err)
	}
	if res.Run.Status != model.PlaybookSucceeded {
		t.Fatalf("run status = %s, want succeeded", res.Run.Status)
	}
	if len(res.Run.TaskIDs) != 1 || len(res.Outcomes) != 1 {
		t.Fatalf("expected 1 step task, got run=%v outcomes=%d", res.Run.TaskIDs, len(res.Outcomes))
	}
	if res.Outcomes[0].Task.Status != model.TaskSucceeded {
		t.Fatalf("step task status = %s", res.Outcomes[0].Task.Status)
	}
}
