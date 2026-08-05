package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

// TestRunTaskResolvesAssetViaProjectHeader guards the manual single-tool run path: a POST /v1/tasks that
// names an asset but not a project (as the Scan tab sends) must still resolve that asset against the active
// project carried by X-Project-Id. On the real split backing (ADR-0049) the asset lives in the project's
// own database, so without the header fallback the engine looks in the global store, fails to find it, and
// the run never starts. This must run on the split manager — the combined test backing returns the same
// handle for global and project and so would mask the bug.
func TestRunTaskResolvesAssetViaProjectHeader(t *testing.T) {
	dir := t.TempDir()
	mgr, err := store.OpenManager(dir, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })

	casr := cas.NewPerProject(mgr.ProjectCASDir)
	engine := task.NewEngine(mgr, casr, capability.BuiltIns(), fakeTaskRunner{})
	srv := httptest.NewServer(New(Deps{Store: mgr, Engine: engine, CASResolver: casr}).Handler())
	t.Cleanup(func() { srv.Close(); engine.Close() })

	do := func(method, path, projectID string, body any, out any) int {
		var rdr *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rdr = bytes.NewReader(b)
		} else {
			rdr = bytes.NewReader(nil)
		}
		req, _ := http.NewRequest(method, srv.URL+path, rdr)
		if projectID != "" {
			req.Header.Set("X-Project-Id", projectID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if out != nil {
			_ = json.NewDecoder(resp.Body).Decode(out)
		}
		return resp.StatusCode
	}

	var proj struct {
		ID string `json:"id"`
	}
	if code := do("POST", "/v1/projects", "", map[string]string{"name": "Alpha"}, &proj); code != http.StatusCreated {
		t.Fatalf("create project = %d, want 201", code)
	}

	// The asset lives in the project's own database — the store split the manual run has to bridge.
	ctx := context.Background()
	pdb, err := mgr.Project(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	app, err := pdb.CreateApplication(ctx, proj.ID, "Store")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := pdb.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: t.TempDir(), Sensitivity: "private"})
	if err != nil {
		t.Fatal(err)
	}

	// The Scan tab's request: an asset_id, no project_id in the body — the project rides in X-Project-Id.
	var created model.Task
	code := do("POST", "/v1/tasks", proj.ID, map[string]any{
		"capability_id": "source-inventory",
		"asset_id":      asset.ID,
		"actor":         "human",
	}, &created)
	if code != http.StatusAccepted {
		t.Fatalf("POST /v1/tasks = %d, want 202 (asset must resolve via X-Project-Id)", code)
	}
	if created.ID == "" || created.Status != model.TaskPending {
		t.Fatalf("enqueued task = %+v, want a pending task with an id", created)
	}

	// And it must actually run to completion against the project asset, not just enqueue.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var got model.Task
		if do("GET", "/v1/tasks/"+created.ID, proj.ID, nil, &got); got.Status == model.TaskSucceeded {
			break
		} else if got.Status == model.TaskFailed {
			t.Fatalf("task failed: %s", got.Error)
		}
		if time.Now().After(deadline) {
			t.Fatal("task never completed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Without any project scope — no header, no body project_id — the asset can't be resolved, so the run
	// must fail fast with a 400 rather than create a task that can never find its target.
	var errBody map[string]any
	if code := do("POST", "/v1/tasks", "", map[string]any{
		"capability_id": "source-inventory",
		"asset_id":      asset.ID,
		"actor":         "human",
	}, &errBody); code != http.StatusBadRequest {
		t.Fatalf("unscoped run = %d, want 400", code)
	}
}
