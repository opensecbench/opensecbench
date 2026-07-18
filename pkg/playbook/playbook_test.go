package playbook

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

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
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), runner.LocalRunner{})

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	repo := t.TempDir()
	_ = os.WriteFile(filepath.Join(repo, "x.go"), []byte("y"), 0o600)
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo})

	res, err := NewRunner(engine, db).Run(ctx, "recon", asset.ID, "human")
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
