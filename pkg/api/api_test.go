package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/task"
)

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(New(Deps{}).Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status field = %q, want ok", body["status"])
	}
}

func newTestServer(t *testing.T) *httptest.Server {
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
	engine := task.NewEngine(db, blobs, capability.BuiltIns(), runner.LocalRunner{})
	srv := httptest.NewServer(New(Deps{Store: db, Engine: engine, CAS: blobs}).Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = db.Close()
	})
	return srv
}

func TestCapabilitiesAPI(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Get(srv.URL + "/v1/capabilities")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var manifests []capability.Manifest
	if err := json.NewDecoder(resp.Body).Decode(&manifests); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, m := range manifests {
		ids = append(ids, m.ID)
	}
	if !contains(ids, "semgrep") || !contains(ids, "source-inventory") {
		t.Fatalf("capabilities missing built-ins: %v", ids)
	}
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func TestProjectAPI(t *testing.T) {
	srv := newTestServer(t)

	// Create.
	body := bytes.NewBufferString(`{"name":"Q3 API Assessment"}`)
	resp, err := http.Post(srv.URL+"/v1/projects", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", resp.StatusCode)
	}
	var created model.Project
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if created.ID == "" || created.Name != "Q3 API Assessment" || created.Status != "active" {
		t.Fatalf("unexpected created project: %+v", created)
	}

	// Get by id.
	resp, err = http.Get(srv.URL + "/v1/projects/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", resp.StatusCode)
	}
	_ = resp.Body.Close()

	// List.
	resp, err = http.Get(srv.URL + "/v1/projects")
	if err != nil {
		t.Fatal(err)
	}
	var list []model.Project
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(list) != 1 {
		t.Fatalf("listed %d projects, want 1", len(list))
	}

	// Missing project is 404.
	resp, err = http.Get(srv.URL + "/v1/projects/nonexistent")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("get missing status = %d, want 404", resp.StatusCode)
	}
}

func TestAnalystAsk(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
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
	if _, err := db.CreateProject(context.Background(), store.NewProject{Name: "Acme"}); err != nil {
		t.Fatal(err)
	}

	// Scripted provider: call the list_projects tool, then answer.
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"list_projects","args":{}}`,
		`{"answer":"There is 1 project named Acme."}`,
	}}
	srv := httptest.NewServer(New(Deps{Store: db, Provider: mock}).Handler())
	defer func() { srv.Close(); _ = db.Close() }()

	resp, err := http.Post(srv.URL+"/v1/analyst/ask", "application/json", bytes.NewBufferString(`{"message":"how many projects?"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var out struct {
		Answer string `json:"answer"`
		Steps  []struct {
			Call struct {
				Tool string `json:"tool"`
			} `json:"call"`
			Approved bool `json:"approved"`
		} `json:"steps"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Answer, "Acme") {
		t.Fatalf("answer = %q", out.Answer)
	}
	if len(out.Steps) != 1 || out.Steps[0].Call.Tool != "list_projects" || !out.Steps[0].Approved {
		t.Fatalf("steps = %+v", out.Steps)
	}
}

func TestAnalystAskWithoutProvider(t *testing.T) {
	srv := httptest.NewServer(New(Deps{}).Handler())
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/v1/analyst/ask", "application/json", bytes.NewBufferString(`{"message":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when no provider", resp.StatusCode)
	}
}

func TestCreateProjectValidation(t *testing.T) {
	srv := newTestServer(t)
	resp, err := http.Post(srv.URL+"/v1/projects", "application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty-name create status = %d, want 400", resp.StatusCode)
	}
}
