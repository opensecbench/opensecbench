package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// A finding can be advanced through its lifecycle via POST /findings/{id}/status (the store method existed
// but had no route before ADR review 2026-07-20).
func TestSetFindingStatus(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := t.Context()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Store")
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: "abc", Size: 1, Kind: model.ArtifactInput, Name: "r"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "SQLi", Severity: "high"})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)
	finding, err := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Authn bypass", Severity: "high", ObservationIDs: []string{obs.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if finding.Status != model.FindingOpen {
		t.Fatalf("new finding should be open, got %q", finding.Status)
	}

	// Advance to remediated.
	var updated model.Finding
	if code := postJSON(t, srv.URL+"/v1/findings/"+finding.ID+"/status", `{"status":"remediated"}`, &updated); code != http.StatusOK {
		t.Fatalf("set status = %d", code)
	}
	if updated.Status != model.FindingRemediated {
		t.Fatalf("status not updated: %+v", updated)
	}

	// Invalid status is rejected.
	if code := postJSON(t, srv.URL+"/v1/findings/"+finding.ID+"/status", `{"status":"bogus"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid status should be 400, got %d", code)
	}
	// Unknown finding is 404.
	if code := postJSON(t, srv.URL+"/v1/findings/nope/status", `{"status":"open"}`, nil); code != http.StatusNotFound {
		t.Fatalf("unknown finding should be 404, got %d", code)
	}
}

// An unknown target on project create is a 400, not a 500 (review #7).
func TestCreateProjectUnknownTargetIs400(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })

	if code := postJSON(t, srv.URL+"/v1/projects", `{"name":"P","target_ids":["does-not-exist"]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("unknown target should be 400, got %d", code)
	}
}
