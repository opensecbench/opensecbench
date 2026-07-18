package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func newEngine(t *testing.T, r runner.Runner) (*Engine, *cas.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewEngine(db, blobs, capability.BuiltIns(), r), blobs
}

// fakeRunner returns canned output without touching Docker, so the provenance wiring can be
// tested deterministically.
type fakeRunner struct {
	out  []byte
	code int
}

func (fakeRunner) Name() string { return "fake" }
func (f fakeRunner) Run(context.Context, runner.RunSpec) (runner.Result, error) {
	return runner.Result{Stdout: f.out, ExitCode: f.code}, nil
}

func TestEngineRecordsProvenance(t *testing.T) {
	const output = "cmd/main.go\npkg/store/store.go\n"
	eng, blobs := newEngine(t, fakeRunner{out: []byte(output), code: 0})

	out, err := eng.Run(context.Background(), RunRequest{
		CapabilityID: "source-inventory",
		TargetDir:    "/some/repo",
		Actor:        "human:test",
	})
	if err != nil {
		t.Fatal(err)
	}

	if out.Task.Status != model.TaskSucceeded {
		t.Fatalf("status = %s, want succeeded (err=%q)", out.Task.Status, out.Task.Error)
	}
	if out.Task.CapabilityID != "source-inventory" || out.Task.CapabilityVersion != "1.0.0" || out.Task.Runner != "fake" {
		t.Fatalf("task provenance wrong: %+v", out.Task)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("got %d artifacts, want 1", len(out.Artifacts))
	}

	art := out.Artifacts[0]
	if art.Name != "inventory.txt" || art.Kind != model.ArtifactOutput || *art.TaskID != out.Task.ID {
		t.Fatalf("artifact wrong: %+v", art)
	}

	// Digest matches the output, and the bytes are retrievable from the CAS.
	want := sha256.Sum256([]byte(output))
	if art.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("artifact digest %s does not match output", art.SHA256)
	}
	rc, err := blobs.Open(art.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, _ := io.ReadAll(rc)
	if string(got) != output {
		t.Fatalf("CAS content = %q, want %q", got, output)
	}
}

const sarifOutput = `{"version":"2.1.0","runs":[{"results":[
  {"ruleId":"r1","level":"error","message":{"text":"Hardcoded secret"},
   "locations":[{"physicalLocation":{"artifactLocation":{"uri":"config.py"},"region":{"startLine":12}}}]},
  {"ruleId":"r2","level":"warning","message":{"text":"Weak hash"},"locations":[]}
]}]}`

func TestEngineInterpretsSARIFIntoObservations(t *testing.T) {
	// semgrep declares SARIF output, so the engine interprets the run's stdout into unreviewed
	// tool observations. The fake runner returns a SARIF document with two results.
	eng, _ := newEngine(t, fakeRunner{out: []byte(sarifOutput), code: 0})

	out, err := eng.Run(context.Background(), RunRequest{CapabilityID: "semgrep", TargetDir: "/x", Actor: "human:test"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Task.Status != model.TaskSucceeded {
		t.Fatalf("status = %s (err=%q)", out.Task.Status, out.Task.Error)
	}
	if len(out.Observations) != 2 {
		t.Fatalf("got %d observations, want 2", len(out.Observations))
	}
	for _, o := range out.Observations {
		if o.Origin != model.OriginTool || o.ReviewState != model.ReviewUnreviewed {
			t.Fatalf("observation not tool/unreviewed: %+v", o)
		}
		if o.TaskID == nil || *o.TaskID != out.Task.ID {
			t.Fatalf("observation not linked to task: %+v", o)
		}
	}
}

func TestEngineMarksNonOKExitFailed(t *testing.T) {
	// source-inventory only accepts exit 0; exit 2 must be recorded as failed, with the output
	// still captured as an artifact for debugging.
	eng, _ := newEngine(t, fakeRunner{out: []byte("boom"), code: 2})
	out, err := eng.Run(context.Background(), RunRequest{CapabilityID: "source-inventory", TargetDir: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Task.Status != model.TaskFailed || out.Task.ExitCode == nil || *out.Task.ExitCode != 2 {
		t.Fatalf("expected failed/exit 2, got %+v", out.Task)
	}
	if len(out.Artifacts) != 1 {
		t.Fatalf("output should still be captured, got %d artifacts", len(out.Artifacts))
	}
}

type sleepCap struct{}

func (sleepCap) Manifest() capability.Manifest {
	return capability.Manifest{ID: "sleep", Version: "1.0.0", OutputName: "out.txt"}
}
func (sleepCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "alpine:3", Cmd: []string{"sleep", "30"}, Timeout: 60 * time.Second}, nil
}

func TestEngineCancelStopsRun(t *testing.T) {
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
	reg := capability.NewRegistry()
	reg.Register(sleepCap{})
	eng := NewEngine(db, blobs, reg, runner.LocalRunner{})

	ctx := context.Background()
	done := make(chan Outcome, 1)
	go func() {
		out, _ := eng.Run(ctx, RunRequest{CapabilityID: "sleep", TargetDir: "/x", Actor: "test"})
		done <- out
	}()

	// Wait for the task to be running, then cancel it.
	var id string
	for i := 0; i < 120; i++ {
		tasks, _ := db.ListTasks(ctx, 5)
		if len(tasks) > 0 && tasks[0].Status == model.TaskRunning {
			id = tasks[0].ID
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if id == "" {
		t.Fatal("task never reached running state")
	}
	if err := eng.Cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	select {
	case out := <-done:
		if out.Task.Status != model.TaskFailed {
			t.Fatalf("cancelled task status = %s, want failed", out.Task.Status)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("run did not return after cancel (container not killed)")
	}

	// Cancelling a task that is no longer running errors.
	if err := eng.Cancel(id); err != ErrTaskNotRunning {
		t.Fatalf("cancel finished task = %v, want ErrTaskNotRunning", err)
	}
}

func TestEngineSourceInventoryInDocker(t *testing.T) {
	if !runner.Available() {
		t.Skip("docker not available")
	}
	// Real end-to-end: a capability runs in a sandbox against a real directory and its output
	// artifact lands in the CAS with provenance.
	repo := t.TempDir()
	for _, f := range []string{"main.go", "README.md"} {
		if err := os.WriteFile(filepath.Join(repo, f), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	eng, blobs := newEngine(t, runner.LocalRunner{})

	out, err := eng.Run(context.Background(), RunRequest{CapabilityID: "source-inventory", TargetDir: repo, Actor: "human:test"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Task.Status != model.TaskSucceeded {
		t.Fatalf("status = %s (err=%q)", out.Task.Status, out.Task.Error)
	}
	rc, err := blobs.Open(out.Artifacts[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	content, _ := io.ReadAll(rc)
	if !strings.Contains(string(content), "main.go") || !strings.Contains(string(content), "README.md") {
		t.Fatalf("inventory missing files: %q", content)
	}
}
