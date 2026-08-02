package task

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// openStore opens a fresh migrated store + CAS so a test can seed task rows before any engine exists —
// simulating work enqueued by a process that has since died (durable-queue tests, ADR-0023).
func openStore(t *testing.T) (*store.DB, *cas.Store) {
	t.Helper()
	db := storetest.New(t)
	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	return db, blobs
}

// A pending task left by a dead process is claimed and run by a freshly-started engine (the row IS the
// queue — no in-memory hand-off is needed to resume it).
func TestDurableResumesPendingTask(t *testing.T) {
	db, blobs := openStore(t)
	task, err := db.CreateTask(context.Background(), store.NewTask{
		CapabilityID: "source-inventory", CapabilityVersion: "1.0.0",
		TargetDir: "/some/repo", Actor: "human", Runner: "fake", Queued: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskPending {
		t.Fatalf("seed status = %s, want pending", task.Status)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("cmd/main.go\n"), code: 0})
	defer eng.Close()

	if done := pollTask(t, eng, task.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("orphaned pending task should resume to succeeded, got %s (err=%q)", done.Status, done.Error)
	}
}

// A task left mid-run (status 'running') by a crash is requeued on startup and resumed.
func TestDurableRequeuesInterruptedRunningTask(t *testing.T) {
	db, blobs := openStore(t)
	task, err := db.CreateTask(context.Background(), store.NewTask{
		CapabilityID: "source-inventory", CapabilityVersion: "1.0.0",
		TargetDir: "/repo", Actor: "human", Runner: "fake", Queued: false, // starts 'running'
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskRunning {
		t.Fatalf("seed status = %s, want running", task.Status)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("x\n"), code: 0})
	defer eng.Close()

	if done := pollTask(t, eng, task.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("interrupted running task should requeue + resume, got %s (err=%q)", done.Status, done.Error)
	}
}

// A task that has already been interrupted maxAttempts times is failed rather than re-run forever — the
// crash-loop backstop for the at-least-once resume policy.
func TestDurableRetryCapFailsCrashLoop(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	task, err := db.CreateTask(ctx, store.NewTask{
		CapabilityID: "source-inventory", CapabilityVersion: "1.0.0",
		TargetDir: "/r", Actor: "human", Runner: "fake", Queued: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Pretend it has already burned the default cap (3) of attempts across prior interruptions.
	if _, err := db.ExecContext(ctx, `UPDATE tasks SET attempts = 3 WHERE id = ?`, task.ID); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("x"), code: 0})
	defer eng.Close()

	done := pollTask(t, eng, task.ID)
	if done.Status != model.TaskFailed {
		t.Fatalf("crash-loop task should be failed, got %s", done.Status)
	}
	if !strings.Contains(done.Error, "exceeded retry") {
		t.Fatalf("error = %q, want an exceeded-retry message", done.Error)
	}
}

// Secret references round-trip through persistence: enqueue with a secret ref, and the worker (which
// reconstructs the request from the DB row, not an in-memory job) resolves + injects + redacts it.
func TestDurableReconstructsSecretRefs(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	const secretVal = "TOKEN-abc-123"
	cr := &capturingRunner{out: []byte("Authorization: Bearer " + secretVal + "\nok\n")}
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), cr)
	eng.Secrets = func(_ context.Context, _ *string, name string) (string, error) {
		if name == "api_token" {
			return secretVal, nil
		}
		return "", store.ErrNotFound
	}
	defer eng.Close()

	task, err := eng.Enqueue(ctx, RunRequest{
		CapabilityID: "http-probe",
		Params:       map[string]any{"target": "https://api.example/health"},
		SecretRefs:   map[string]string{"AUTH_TOKEN": "api_token"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if done := pollTask(t, eng, task.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("status = %s (err=%q)", done.Status, done.Error)
	}

	// The ref name is persisted (never the value).
	row, _ := db.GetTask(ctx, task.ID)
	if row.SecretRefs["AUTH_TOKEN"] != "api_token" {
		t.Fatalf("persisted secret ref = %+v, want AUTH_TOKEN->api_token", row.SecretRefs)
	}
	// The output artifact proves the value was resolved from that ref (redacted, not leaked) — i.e. the
	// secret ref survived the persist→claim→reconstruct round-trip. Read via CAS after completion.
	arts, _ := db.ListArtifactsByTask(ctx, task.ID)
	if len(arts) == 0 {
		t.Fatal("no output artifact")
	}
	rc, err := blobs.Open(arts[0].SHA256)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	content, _ := io.ReadAll(rc)
	if strings.Contains(string(content), secretVal) {
		t.Fatalf("secret value leaked into artifact: %q", content)
	}
	if !strings.Contains(string(content), "«redacted:api_token»") {
		t.Fatalf("expected redaction marker (secret ref reconstructed + injected), got: %q", content)
	}
}
