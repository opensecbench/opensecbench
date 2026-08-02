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

// TestExportImportFullAPI verifies the ?full=true query param plumbs full-mode working state through the
// HTTP layer (ADR-0060): a full-only entity (an Analyst thread + message) survives a full export/import
// round-trip, and does NOT leak through the default shareable export.
func TestExportImportFullAPI(t *testing.T) {
	src, db := serverWithDB(t)
	ctx := t.Context()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Full"})
	th, _ := db.CreateThread(ctx, store.NewThread{ProjectID: &proj.ID, Title: "Analyst chat", Provider: "anthropic"})
	if _, err := db.AppendMessage(ctx, th.ID, "user", "hello analyst"); err != nil {
		t.Fatal(err)
	}

	// export sends the bundle bytes for the given query suffix ("" = shareable, "?full=true" = full).
	export := func(query string) []byte {
		req, _ := http.NewRequest(http.MethodPost, src.URL+"/v1/projects/"+proj.ID+"/export"+query, nil)
		req.Header.Set("X-OSB-Passphrase", "hunter2")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := readAll(resp)
		if resp.StatusCode != http.StatusOK || len(b) == 0 {
			t.Fatalf("export%q = %d, %d bytes", query, resp.StatusCode, len(b))
		}
		return b
	}
	// importInto imports bundle bytes into a fresh instance and returns its store for assertions.
	importInto := func(data []byte) *store.DB {
		dst, ddb := serverWithDB(t)
		req, _ := http.NewRequest(http.MethodPost, dst.URL+"/v1/import", bytes.NewReader(data))
		req.Header.Set("X-OSB-Passphrase", "hunter2")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d", resp.StatusCode)
		}
		return ddb
	}

	// Shareable export must NOT carry the Analyst thread.
	if ddb := importInto(export("")); len(mustThreads(t, ddb)) != 0 {
		t.Fatalf("shareable export leaked %d threads", len(mustThreads(t, ddb)))
	}

	// Full export carries the thread and its messages.
	ddb := importInto(export("?full=true"))
	threads := mustThreads(t, ddb)
	if len(threads) != 1 || threads[0].Title != "Analyst chat" {
		t.Fatalf("full export threads = %+v", threads)
	}
	msgs, _ := ddb.ListMessages(ctx, threads[0].ID)
	if len(msgs) != 1 || msgs[0].Content != "hello analyst" {
		t.Fatalf("full export messages = %+v", msgs)
	}
}

func mustThreads(t *testing.T, db *store.DB) []model.Thread {
	t.Helper()
	th, err := db.ListThreads(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func readAll(resp *http.Response) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	var b bytes.Buffer
	_, err := b.ReadFrom(resp.Body)
	return b.Bytes(), err
}
