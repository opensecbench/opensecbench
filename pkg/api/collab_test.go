package api

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

func serverWithDB(t *testing.T) (*httptest.Server, *store.DB) {
	t.Helper()
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	key := make([]byte, secret.KeySize)
	_, _ = rand.Read(key)
	vault, _ := secret.NewVault(key)
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs, Vault: vault}).Handler())
	t.Cleanup(func() { srv.Close() })
	return srv, db
}

func TestExportImportAPI(t *testing.T) {
	src, db := serverWithDB(t)
	ctx := t.Context()

	// Seed a project with a confirmed-evidence finding.
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Shareable"})
	app, _ := db.CreateApplication(ctx, proj.ID, "App")
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: "deadbeef", Size: 1, Kind: model.ArtifactInput, Name: "e"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "SQLi", Severity: "high"})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)
	_, _ = db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high", ObservationIDs: []string{obs.ID}})

	// Export requires the passphrase header.
	if code := postJSON(t, src.URL+"/v1/projects/"+proj.ID+"/export", ``, nil); code != http.StatusBadRequest {
		t.Fatalf("export without passphrase = %d, want 400", code)
	}

	// Export with the header.
	req, _ := http.NewRequest(http.MethodPost, src.URL+"/v1/projects/"+proj.ID+"/export", nil)
	req.Header.Set("X-OSB-Passphrase", "hunter2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	bundleBytes, _ := readAll(resp)
	if resp.StatusCode != http.StatusOK || len(bundleBytes) == 0 {
		t.Fatalf("export = %d, %d bytes", resp.StatusCode, len(bundleBytes))
	}

	// Import into a second instance.
	dst, ddb := serverWithDB(t)
	ireq, _ := http.NewRequest(http.MethodPost, dst.URL+"/v1/import", bytes.NewReader(bundleBytes))
	ireq.Header.Set("X-OSB-Passphrase", "hunter2")
	iresp, err := http.DefaultClient.Do(ireq)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = iresp.Body.Close() }()
	if iresp.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d", iresp.StatusCode)
	}
	var out struct {
		ProjectID string `json:"project_id"`
	}
	_ = json.NewDecoder(iresp.Body).Decode(&out)

	np, _ := ddb.GetProject(ctx, out.ProjectID)
	if np.Name != "Shareable" {
		t.Fatalf("imported project wrong: %+v", np)
	}
	findings, _ := ddb.ListFindings(ctx)
	if len(findings) != 1 || findings[0].Title != "Auth bypass" {
		t.Fatalf("finding not imported: %+v", findings)
	}
}

func readAll(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, err := b.ReadFrom(resp.Body)
	return b.Bytes(), err
}
