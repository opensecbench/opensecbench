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
		Answer      string `json:"answer"`
		NewMessages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"new_messages"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Answer, "Acme") {
		t.Fatalf("answer = %q", out.Answer)
	}
	// The transcript should record the read-only tool activity (auto-approved).
	tooled := false
	for _, m := range out.NewMessages {
		if strings.Contains(m.Content, "list_projects") {
			tooled = true
		}
	}
	if !tooled {
		t.Fatalf("no tool activity in transcript: %+v", out.NewMessages)
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

// postJSON is a small helper for the Repeater test: POST a JSON body and decode the response.
func postJSON(t *testing.T, url, body string, out any) int {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if out != nil && resp.StatusCode < http.StatusBadRequest {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			t.Fatalf("decode: %v", err)
		}
	}
	return resp.StatusCode
}

func TestRepeaterScopeGuardedSend(t *testing.T) {
	srv := newTestServer(t)

	// A target that records whether it was reached.
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hit = true
		w.Header().Set("X-Origin", "target")
		_, _ = w.Write([]byte("pong"))
	}))
	defer target.Close()

	var proj model.Project
	if code := postJSON(t, srv.URL+"/v1/projects", `{"name":"repeater"}`, &proj); code != http.StatusCreated {
		t.Fatalf("create project = %d", code)
	}

	// Scope the project to loopback only (the httptest target host).
	var entry model.ScopeEntry
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/scope", `{"kind":"host","value":"127.0.0.1"}`, &entry); code != http.StatusCreated {
		t.Fatalf("add scope = %d", code)
	}

	// In-scope exchange sends and captures the response.
	var ex model.HTTPExchange
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/exchanges",
		`{"method":"GET","url":"`+target.URL+`"}`, &ex); code != http.StatusCreated {
		t.Fatalf("create exchange = %d", code)
	}
	var sent model.HTTPExchange
	if code := postJSON(t, srv.URL+"/v1/exchanges/"+ex.ID+"/send", ``, &sent); code != http.StatusOK {
		t.Fatalf("send in-scope = %d, want 200", code)
	}
	if !hit || sent.Status == nil || *sent.Status != http.StatusOK || !strings.Contains(sent.ResponseBody, "pong") {
		t.Fatalf("in-scope send did not capture response: hit=%v ex=%+v", hit, sent)
	}

	// Save the response as evidence → a human-origin observation.
	var obs model.Observation
	if code := postJSON(t, srv.URL+"/v1/exchanges/"+ex.ID+"/evidence", `{"note":"interesting header"}`, &obs); code != http.StatusCreated {
		t.Fatalf("save evidence = %d, want 201", code)
	}
	if obs.Origin != model.OriginHuman || obs.ArtifactID == nil {
		t.Fatalf("evidence observation wrong: %+v", obs)
	}

	// Out-of-scope exchange is blocked before any request is sent.
	hit = false
	var bad model.HTTPExchange
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/exchanges",
		`{"method":"GET","url":"http://example.invalid/x"}`, &bad); code != http.StatusCreated {
		t.Fatalf("create bad exchange = %d", code)
	}
	if code := postJSON(t, srv.URL+"/v1/exchanges/"+bad.ID+"/send", ``, nil); code != http.StatusForbidden {
		t.Fatalf("out-of-scope send = %d, want 403", code)
	}
	if hit {
		t.Fatal("out-of-scope target should never be reached")
	}
}
