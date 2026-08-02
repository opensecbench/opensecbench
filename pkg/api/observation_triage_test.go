package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// TestObservationTriageEndpoints drives the real Handler() for the Observations surface's human triage
// actions: promote (confirm + create a finding) and investigate (open an investigation). Both run through
// routing + CORS, and both must reach the right project/app.
func TestObservationTriageEndpoints(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng"})

	post := func(path string) *http.Response {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, nil)
		req.Header.Set("X-Project-Id", proj.ID)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// Promote: an unreviewed tool observation becomes a confirmed finding in one action.
	o1, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Origin: model.OriginTool, Title: "SQLi", Severity: "high", Location: "app/db.py:42"})
	resp := post("/v1/observations/" + o1.ID + "/promote")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("promote status = %d, want 201", resp.StatusCode)
	}
	var f model.Finding
	_ = json.NewDecoder(resp.Body).Decode(&f)
	_ = resp.Body.Close()
	if f.Title != "SQLi" || f.Severity != "high" {
		t.Fatalf("promoted finding = %+v", f)
	}
	if got, _ := db.GetObservation(ctx, o1.ID); got.ReviewState != model.ReviewConfirmed {
		t.Fatalf("observation review state = %q, want confirmed", got.ReviewState)
	}
	if fs, _ := db.ListFindings(ctx); len(fs) != 1 {
		t.Fatalf("findings after promote = %d, want 1", len(fs))
	}

	// Investigate: a second observation opens an investigation, idempotently.
	o2, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Origin: model.OriginTool, Title: "weak hash", Severity: "medium"})
	resp = post("/v1/observations/" + o2.ID + "/investigate")
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("investigate status = %d, want 201", resp.StatusCode)
	}
	_ = resp.Body.Close()
	resp = post("/v1/observations/" + o2.ID + "/investigate") // idempotent — no second row
	_ = resp.Body.Close()
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(invs) != 1 || invs[0].ObservationID != o2.ID {
		t.Fatalf("investigations = %+v, want exactly one for %s", invs, o2.ID)
	}

	// Promoting a non-existent observation is a 404, not a 500.
	resp = post("/v1/observations/nope/promote")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("promote missing = %d, want 404", resp.StatusCode)
	}
	_ = resp.Body.Close()
}
