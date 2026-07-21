package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/integration"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func putJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPut, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < http.StatusBadRequest {
		_ = json.NewDecoder(resp.Body).Decode(out)
	}
	return resp.StatusCode
}

func newIntegrationServer(t *testing.T) (*httptest.Server, *store.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	vault, err := secret.LoadVault(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs, Vault: vault}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	return srv, db
}

func TestIntegrationConfigAndPull(t *testing.T) {
	// A DefectDojo stub with two findings (one verified).
	dd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Token secret-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"results":[
			{"id":1,"title":"SQLi","severity":"High","description":"d1","verified":true},
			{"id":2,"title":"XSS","severity":"Medium","description":"d2","verified":false}
		]}`))
	}))
	defer dd.Close()

	srv, db := newIntegrationServer(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})

	// Seal the DefectDojo API token in the vault (the connector stores only its name).
	if code := postJSON(t, srv.URL+"/v1/secrets", `{"name":"dd_token","value":"secret-token"}`, &struct{}{}); code >= 300 {
		t.Fatalf("set secret = %d", code)
	}

	// Create a global DefectDojo connector, then bind this project to it with a project-side scope.
	var conn struct {
		ID string `json:"id"`
	}
	if code := postJSON(t, srv.URL+"/v1/connectors", `{"name":"DD","type":"defectdojo","base_url":"`+dd.URL+`","credential":"dd_token"}`, &conn); code != http.StatusCreated {
		t.Fatalf("create connector = %d", code)
	}
	base := srv.URL + "/v1/projects/" + proj.ID + "/integrations/" + conn.ID
	if code := putJSON(t, base, `{"project_key":"9"}`, &struct{}{}); code != http.StatusOK {
		t.Fatalf("bind = %d", code)
	}

	// Pull: two observations imported, correct review states.
	var res struct {
		Imported int `json:"imported"`
		Skipped  int `json:"skipped"`
		Total    int `json:"total"`
	}
	if code := postJSON(t, base+"/pull", `{}`, &res); code != http.StatusOK {
		t.Fatalf("pull = %d", code)
	}
	if res.Imported != 2 || res.Skipped != 0 || res.Total != 2 {
		t.Fatalf("first pull = %+v, want imported 2", res)
	}

	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	if len(obs) != 2 {
		t.Fatalf("project observations = %d, want 2", len(obs))
	}
	var verified, unreviewed int
	for _, o := range obs {
		if o.Origin != "tool" || o.ProjectID == nil || *o.ProjectID != proj.ID {
			t.Fatalf("imported observation not project-scoped tool origin: %+v", o)
		}
		switch o.ReviewState {
		case "confirmed":
			verified++
		case "unreviewed":
			unreviewed++
		}
	}
	if verified != 1 || unreviewed != 1 {
		t.Fatalf("review states: confirmed=%d unreviewed=%d, want 1/1", verified, unreviewed)
	}

	// Re-pull is idempotent — nothing new imported.
	if code := postJSON(t, base+"/pull", `{}`, &res); code != http.StatusOK {
		t.Fatalf("re-pull = %d", code)
	}
	if res.Imported != 0 || res.Skipped != 2 {
		t.Fatalf("re-pull = %+v, want imported 0 / skipped 2", res)
	}
}

func TestPullUnsupportedIntegration(t *testing.T) {
	srv, db := newIntegrationServer(t)
	proj, _ := db.CreateProject(context.Background(), store.NewProject{Name: "p"})
	// jira is push-only — a jira connector can't be pulled.
	var conn struct {
		ID string `json:"id"`
	}
	if code := postJSON(t, srv.URL+"/v1/connectors", `{"name":"J","type":"jira","base_url":"https://jira.local"}`, &conn); code != http.StatusCreated {
		t.Fatalf("create connector = %d", code)
	}
	base := srv.URL + "/v1/projects/" + proj.ID + "/integrations/" + conn.ID
	var body map[string]any
	if code := postJSON(t, base+"/pull", `{}`, &body); code != http.StatusBadRequest {
		t.Fatalf("pull jira = %d, want 400 (push-only)", code)
	}
	// Ensure the connector genuinely lacks Pull (guards the 400 above).
	if c, _ := integration.BuiltIns().Get("jira"); func() bool { _, ok := c.(integration.Puller); return ok }() {
		t.Fatal("jira unexpectedly implements Puller")
	}
}
