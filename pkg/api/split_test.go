package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestSplitModeEndToEnd exercises the real two-tier backing (ADR-0049): a project lives in its own
// on-disk database, writes are routed to it by the X-Project-Id header, projects are isolated, and
// cross-project inboxes fan out. This is the flip's validation — the rest of the suite runs combined.
func TestSplitModeEndToEnd(t *testing.T) {
	dir := t.TempDir()
	mgr, err := store.OpenManager(dir, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	srv := httptest.NewServer(New(Deps{Store: mgr, CASResolver: cas.NewPerProject(dir)}).Handler())
	t.Cleanup(srv.Close)

	// do issues a request with the active-project header and decodes the JSON response.
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

	// Create two projects via the Manager lifecycle.
	var pA, pB struct {
		ID string `json:"id"`
	}
	if code := do("POST", "/v1/projects", "", map[string]string{"name": "Alpha"}, &pA); code != http.StatusCreated {
		t.Fatalf("create A = %d, want 201", code)
	}
	if code := do("POST", "/v1/projects", "", map[string]string{"name": "Bravo"}, &pB); code != http.StatusCreated {
		t.Fatalf("create B = %d, want 201", code)
	}

	// Each project's database exists on disk in its own directory.
	for _, id := range []string{pA.ID, pB.ID} {
		if _, err := os.Stat(filepath.Join(dir, "projects", id, "project.db")); err != nil {
			t.Errorf("project.db missing for %s: %v", id, err)
		}
	}

	// Create an application + finding in project A, routed by the header.
	var appA struct {
		ID string `json:"id"`
	}
	if code := do("POST", "/v1/projects/"+pA.ID+"/applications", pA.ID, map[string]string{"name": "web"}, &appA); code != http.StatusCreated {
		t.Fatalf("create app = %d, want 201", code)
	}
	var find struct {
		ID string `json:"id"`
	}
	if code := do("POST", "/v1/findings", pA.ID, map[string]any{"application_id": appA.ID, "title": "SQLi", "severity": "high"}, &find); code != http.StatusCreated {
		t.Fatalf("create finding = %d, want 201", code)
	}

	// The finding lives only in project A's database — project B sees none.
	aDB, _ := mgr.Project(pA.ID)
	bDB, _ := mgr.Project(pB.ID)
	if n := count(t, aDB, "findings"); n != 1 {
		t.Errorf("A findings = %d, want 1", n)
	}
	if n := count(t, bDB, "findings"); n != 0 {
		t.Errorf("B findings = %d, want 0 (isolation breach)", n)
	}

	// The cross-project inbox fans out and includes A's finding.
	var all []map[string]any
	if code := do("GET", "/v1/findings", "", nil, &all); code != http.StatusOK {
		t.Fatalf("list findings = %d, want 200", code)
	}
	if len(all) != 1 {
		t.Errorf("cross-project findings = %d, want 1", len(all))
	}

	// The global project index lists both projects.
	rows, err := mgr.Global().ListProjectIndex(t.Context())
	if err != nil || len(rows) != 2 {
		t.Fatalf("project index = %v (err %v), want 2", rows, err)
	}

	// Ingesting a document into project A writes its blob under projects/A/cas — not a shared store.
	req, _ := http.NewRequest("POST",
		srv.URL+"/v1/projects/"+pA.ID+"/context?name=doc&type=document", bytes.NewReader([]byte("secret notes")))
	req.Header.Set("X-Project-Id", pA.ID)
	req.Header.Set("Content-Type", "text/plain")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ingest context = %d, want 201", resp.StatusCode)
	}
	casDir := filepath.Join(dir, "projects", pA.ID, "cas")
	if entries, err := os.ReadDir(casDir); err != nil || len(entries) == 0 {
		t.Errorf("project A CAS %s empty or missing (err %v) — blob not routed to per-project store", casDir, err)
	}
	// Project B's CAS stays empty.
	if entries, _ := os.ReadDir(filepath.Join(dir, "projects", pB.ID, "cas")); len(entries) != 0 {
		t.Errorf("project B CAS unexpectedly has %d entries", len(entries))
	}
}

func count(t *testing.T, db *store.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count(%s): %v", table, err)
	}
	return n
}
