package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// fakeTaskRunner returns canned stdout without touching Docker, so async execution can be exercised.
type fakeTaskRunner struct{}

func (fakeTaskRunner) Name() string { return "fake" }
func (fakeTaskRunner) Run(context.Context, runner.RunSpec) (runner.Result, error) {
	return runner.Result{Stdout: []byte("cmd/main.go\n"), ExitCode: 0}, nil
}

func newAsyncTaskServer(t *testing.T) (*httptest.Server, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
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
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), fakeTaskRunner{})
	srv := httptest.NewServer(New(Deps{Store: db, Engine: engine, CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); engine.Close(); _ = db.Close() })
	return srv, db
}

func TestRunTaskEnqueuesAsync(t *testing.T) {
	srv, _ := newAsyncTaskServer(t)

	// POST returns 202 Accepted with a pending task — it does not block on the run.
	var created model.Task
	code := postJSON(t, srv.URL+"/v1/tasks", `{"capability_id":"source-inventory","target_dir":"/x","actor":"human"}`, &created)
	if code != http.StatusAccepted {
		t.Fatalf("POST /v1/tasks = %d, want 202", code)
	}
	if created.ID == "" || created.Status != model.TaskPending {
		t.Fatalf("enqueued task = %+v, want a pending task with an id", created)
	}

	// Polling GET /v1/tasks/{id} shows the worker carry it to a terminal (succeeded) status.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got model.Task
		resp, err := http.Get(srv.URL + "/v1/tasks/" + created.ID)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.NewDecoder(resp.Body).Decode(&got)
		_ = resp.Body.Close()
		if got.Status == model.TaskSucceeded {
			break
		}
		if got.Status == model.TaskFailed {
			t.Fatalf("task failed: %s", got.Error)
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never completed (last status %q)", got.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestRunTaskUnknownCapabilityFailsFast(t *testing.T) {
	srv, db := newAsyncTaskServer(t)

	var body map[string]any
	code := postJSON(t, srv.URL+"/v1/tasks", `{"capability_id":"does-not-exist"}`, &body)
	if code != http.StatusBadRequest {
		t.Fatalf("unknown capability POST = %d, want 400", code)
	}
	// No task should have been created for a rejected request.
	if tasks, _ := db.ListTasks(context.Background(), 10); len(tasks) != 0 {
		t.Fatalf("a rejected enqueue should create no task, got %d", len(tasks))
	}
}
