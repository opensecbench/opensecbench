package task

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), r)
	// Stop the worker pool before the DB closes (Cleanup is LIFO). Without this the engine's workers leak
	// and busy-poll the closed DB for the rest of the run — starving other tests and causing flakes.
	t.Cleanup(eng.Close)
	return eng, blobs
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

func TestEngineScopeGuard(t *testing.T) {
	// A network capability (http-probe) is blocked when its target is not in the project's
	// allowlist, allowed when it matches, and unrestricted when the project has no allowlist.
	eng, _ := newEngine(t, fakeRunner{out: []byte("HTTP/2 200\n"), code: 0})
	proj, err := eng.mgr.Global().CreateProject(context.Background(), store.NewProject{Name: "engagement"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.mgr.Global().AddScopeEntry(context.Background(), proj.ID, "domain", "acme.com", "allow"); err != nil {
		t.Fatal(err)
	}

	// Out of scope: blocked before the runner, recorded as a failed task.
	out, err := eng.Run(context.Background(), RunRequest{
		CapabilityID: "http-probe",
		ProjectID:    &proj.ID,
		Params:       map[string]any{"target": "https://evil.example/"},
	})
	if !errors.Is(err, ErrOutOfScope) {
		t.Fatalf("out-of-scope run err = %v, want ErrOutOfScope", err)
	}
	if out.Task.Status != model.TaskFailed || len(out.Artifacts) != 0 {
		t.Fatalf("blocked task should be failed with no artifacts, got %+v / %d artifacts", out.Task, len(out.Artifacts))
	}

	// In scope: proceeds to the runner and succeeds.
	out, err = eng.Run(context.Background(), RunRequest{
		CapabilityID: "http-probe",
		ProjectID:    &proj.ID,
		Params:       map[string]any{"target": "https://www.acme.com/health"},
	})
	if err != nil {
		t.Fatalf("in-scope run err = %v", err)
	}
	if out.Task.Status != model.TaskSucceeded {
		t.Fatalf("in-scope status = %s (err=%q)", out.Task.Status, out.Task.Error)
	}

	// A project with no scope entries imposes no restriction.
	empty, err := eng.mgr.Global().CreateProject(context.Background(), store.NewProject{Name: "unscoped"})
	if err != nil {
		t.Fatal(err)
	}
	out, err = eng.Run(context.Background(), RunRequest{
		CapabilityID: "http-probe",
		ProjectID:    &empty.ID,
		Params:       map[string]any{"target": "https://anything.example/"},
	})
	if err != nil || out.Task.Status != model.TaskSucceeded {
		t.Fatalf("unscoped run should succeed, got status=%s err=%v", out.Task.Status, err)
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
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), reg, runner.LocalRunner{})
	defer eng.Close()

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

// pollTask waits for a task to reach a terminal status, returning it (or failing on timeout).
func pollTask(t *testing.T, eng *Engine, id string) model.Task {
	t.Helper()
	for i := 0; i < 200; i++ {
		task, err := eng.mgr.Global().GetTask(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == model.TaskSucceeded || task.Status == model.TaskFailed {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s never reached a terminal status", id)
	return model.Task{}
}

func TestEngineEnqueueRunsAsync(t *testing.T) {
	eng, _ := newEngine(t, fakeRunner{out: []byte(sarifOutput), code: 0})
	defer eng.Close()

	// Enqueue returns immediately with a pending task (not yet executed).
	task, err := eng.Enqueue(context.Background(), RunRequest{CapabilityID: "semgrep", TargetDir: "/x", Actor: "human:test"})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskPending {
		t.Fatalf("enqueued task status = %s, want pending", task.Status)
	}

	// A worker picks it up and runs it to completion; the outcome (artifact + observations) lands.
	done := pollTask(t, eng, task.ID)
	if done.Status != model.TaskSucceeded {
		t.Fatalf("async task status = %s (err=%q)", done.Status, done.Error)
	}
	if done.StartedAt == nil {
		t.Fatal("started_at should be set once a worker claimed the task")
	}
	obs, _ := eng.mgr.Global().ListObservationsByTask(context.Background(), task.ID)
	if len(obs) != 2 {
		t.Fatalf("got %d observations from async run, want 2", len(obs))
	}
}

func TestEngineEnqueueBadRequestFailsFast(t *testing.T) {
	eng, _ := newEngine(t, fakeRunner{out: []byte("x"), code: 0})
	defer eng.Close()
	// An unknown capability is rejected before any task is created.
	if _, err := eng.Enqueue(context.Background(), RunRequest{CapabilityID: "nope"}); err == nil {
		t.Fatal("expected an error for an unknown capability")
	}
	if tasks, _ := eng.mgr.Global().ListTasks(context.Background(), 10); len(tasks) != 0 {
		t.Fatalf("a bad enqueue should create no task, got %d", len(tasks))
	}
}

// gateRunner blocks in Run until released, signalling when it has started — so a test can hold the
// single worker busy and prove a second task stays queued.
type gateRunner struct {
	started chan struct{}
	release chan struct{}
}

func (*gateRunner) Name() string { return "gate" }
func (g *gateRunner) Run(ctx context.Context, _ runner.RunSpec) (runner.Result, error) {
	g.started <- struct{}{}
	select {
	case <-g.release:
		return runner.Result{Stdout: []byte("ok"), ExitCode: 0}, nil
	case <-ctx.Done():
		return runner.Result{}, ctx.Err()
	}
}

func TestEngineCancelQueuedTask(t *testing.T) {
	t.Setenv("OSB_TASK_WORKERS", "1") // a single worker so the second task is forced to queue
	gate := &gateRunner{started: make(chan struct{}, 1), release: make(chan struct{})}
	eng, _ := newEngine(t, gate)
	defer eng.Close() // cancels the engine context, unblocking any stuck gate runner via ctx.Done()

	// First task occupies the only worker and blocks.
	busy, err := eng.Enqueue(context.Background(), RunRequest{CapabilityID: "source-inventory", TargetDir: "/a"})
	if err != nil {
		t.Fatal(err)
	}
	<-gate.started // the worker is now blocked inside Run

	// Second task cannot start — it sits in the queue.
	queued, err := eng.Enqueue(context.Background(), RunRequest{CapabilityID: "source-inventory", TargetDir: "/b"})
	if err != nil {
		t.Fatal(err)
	}

	// Cancelling the queued task records it cancelled without ever running it.
	if err := eng.Cancel(queued.ID); err != nil {
		t.Fatalf("cancel queued: %v", err)
	}
	got, _ := eng.mgr.Global().GetTask(context.Background(), queued.ID)
	if got.Status != model.TaskFailed || got.Error != "cancelled by user" {
		t.Fatalf("cancelled queued task = %+v, want failed/cancelled by user", got)
	}
	if got.StartedAt != nil {
		t.Fatal("a cancelled-while-queued task should never have started")
	}

	// Release the busy task; it completes normally, and the cancelled one is skipped by the worker.
	close(gate.release)
	if done := pollTask(t, eng, busy.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("busy task status = %s", done.Status)
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

// capturingRunner records the spec it was given and echoes a canned stdout.
type capturingRunner struct {
	spec runner.RunSpec
	out  []byte
}

func (c *capturingRunner) Name() string { return "capturing" }
func (c *capturingRunner) Run(_ context.Context, spec runner.RunSpec) (runner.Result, error) {
	c.spec = spec
	return runner.Result{Stdout: c.out, ExitCode: 0}, nil
}

func TestEngineInjectsSecretsAndRedactsOutput(t *testing.T) {
	// http-probe (network cap) with a secret ref; the fake runner echoes output containing the value.
	const secretVal = "TOKEN-abc-123"
	cr := &capturingRunner{out: []byte("Authorization: Bearer " + secretVal + "\nok\n")}
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	t.Cleanup(func() { _ = db.Close() })
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), cr)
	t.Cleanup(eng.Close)
	eng.Secrets = func(_ context.Context, _ *string, name string) (string, error) {
		if name == "api_token" {
			return secretVal, nil
		}
		return "", store.ErrNotFound
	}

	out, err := eng.Run(context.Background(), RunRequest{
		CapabilityID: "http-probe",
		Params:       map[string]any{"target": "https://api.example/health"},
		SecretRefs:   map[string]string{"AUTH_TOKEN": "api_token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	// The secret was injected by name only (value in SecretEnv, not on the command line).
	if cr.spec.SecretEnv["AUTH_TOKEN"] != secretVal {
		t.Fatalf("secret not injected into SecretEnv: %+v", cr.spec.SecretEnv)
	}
	// The captured artifact has the value redacted.
	rc, err := blobs.Open(out.Artifacts[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	content, _ := io.ReadAll(rc)
	if strings.Contains(string(content), secretVal) {
		t.Fatalf("secret value leaked into stored artifact: %q", content)
	}
	if !strings.Contains(string(content), "«redacted:api_token»") {
		t.Fatalf("expected redaction marker, got: %q", content)
	}
}

func TestEngineSecretRefsWithoutVaultFails(t *testing.T) {
	eng, _ := newEngine(t, fakeRunner{out: []byte("x"), code: 0})
	// No eng.Secrets configured.
	out, err := eng.Run(context.Background(), RunRequest{
		CapabilityID: "http-probe",
		Params:       map[string]any{"target": "https://x/"},
		SecretRefs:   map[string]string{"T": "tok"},
	})
	if err == nil {
		t.Fatal("expected error when secret refs requested without a vault")
	}
	if out.Task.Status != model.TaskFailed {
		t.Fatalf("task should be failed, got %s", out.Task.Status)
	}
}

func TestEngineTechniqueGate(t *testing.T) {
	// nmap is tagged technique="intrusive" (ADR-0051). It is blocked at enqueue when the engagement does
	// not permit intrusive testing, allowed when it does, and unconstrained when no engagement is set.
	eng, _ := newEngine(t, fakeRunner{out: []byte("<nmaprun/>"), code: 0})
	ctx := context.Background()
	g := eng.mgr.Global()
	proj, err := g.CreateProject(ctx, store.NewProject{Name: "engagement"})
	if err != nil {
		t.Fatal(err)
	}

	// No engagement yet → unconstrained (fail-open): the run is not blocked by the technique gate. It still
	// needs an in-scope target, so give it one.
	if _, err := g.AddScopeEntry(ctx, proj.ID, "domain", "acme.com", "allow"); err != nil {
		t.Fatal(err)
	}
	run := func() error {
		_, err := eng.Run(ctx, RunRequest{CapabilityID: "nmap", ProjectID: &proj.ID, Params: map[string]any{"target": "scan.acme.com"}})
		return err
	}
	if err := run(); errors.Is(err, ErrTechniqueNotPermitted) {
		t.Fatal("no engagement should be unconstrained, but was blocked")
	}

	// Engagement that disallows intrusive → blocked at enqueue with ErrTechniqueNotPermitted.
	if _, err := g.SetEngagement(ctx, model.Engagement{ProjectID: proj.ID, Techniques: map[string]bool{"intrusive": false}}); err != nil {
		t.Fatal(err)
	}
	if err := run(); !errors.Is(err, ErrTechniqueNotPermitted) {
		t.Fatalf("disallowed technique run err = %v, want ErrTechniqueNotPermitted", err)
	}

	// Engagement that allows intrusive → passes the technique gate.
	if _, err := g.SetEngagement(ctx, model.Engagement{ProjectID: proj.ID, Techniques: map[string]bool{"intrusive": true}}); err != nil {
		t.Fatal(err)
	}
	if err := run(); errors.Is(err, ErrTechniqueNotPermitted) {
		t.Fatal("permitted technique should not be blocked")
	}
}
