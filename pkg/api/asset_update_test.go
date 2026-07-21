package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestUpdateAssetSensitivityEndpoint drives the real Handler() (routing + CORS) to confirm the
// PUT /v1/assets/{id} verb reaches the handler and edits sensitivity — PATCH would be rejected by the
// CORS allow-methods list, which is why the endpoint uses PUT.
func TestUpdateAssetSensitivityEndpoint(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Store")
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/oss/thing"})
	if asset.Sensitivity != model.SensitivityOpenSource {
		t.Fatalf("setup sensitivity = %q, want open_source", asset.Sensitivity)
	}

	body, _ := json.Marshal(map[string]string{"sensitivity": model.SensitivityPrivate})
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/assets/"+asset.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got model.Asset
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Sensitivity != model.SensitivityPrivate {
		t.Fatalf("returned sensitivity = %q, want private", got.Sensitivity)
	}

	// Verify persistence.
	reloaded, _ := db.GetAsset(ctx, asset.ID)
	if reloaded.Sensitivity != model.SensitivityPrivate {
		t.Fatal("update did not persist")
	}

	// A bad value is a 400, not a silent no-op.
	bad, _ := json.Marshal(map[string]string{"sensitivity": "bogus"})
	req2, _ := http.NewRequest(http.MethodPut, srv.URL+"/v1/assets/"+asset.ID, bytes.NewReader(bad))
	req2.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad-value status = %d, want 400", resp2.StatusCode)
	}
}
