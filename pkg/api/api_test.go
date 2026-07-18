package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(New(nil).Handler())
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
	srv := httptest.NewServer(New(db).Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = db.Close()
	})
	return srv
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
