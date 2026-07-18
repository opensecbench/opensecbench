package api

import (
	"net/http"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestMethodologyCoverageFlow(t *testing.T) {
	srv := newTestServer(t)

	// Catalog is available.
	var catalog []methodology.Methodology
	postGet(t, srv.URL+"/v1/methodologies", &catalog)
	if len(catalog) < 3 {
		t.Fatalf("catalog packs = %d, want >=3", len(catalog))
	}

	var proj model.Project
	postJSON(t, srv.URL+"/v1/projects", `{"name":"web engagement"}`, &proj)

	// Adopt a pack.
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/methodology/adopt", `{"methodology_id":"oidc-oauth"}`, nil); code != http.StatusNoContent {
		t.Fatalf("adopt = %d", code)
	}
	// Unknown pack rejected.
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/methodology/adopt", `{"methodology_id":"nope"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("adopt unknown = %d, want 400", code)
	}

	// Set coverage on two items.
	postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/coverage", `{"item_id":"oidc-oauth/pkce","status":"covered"}`, nil)
	postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/coverage", `{"item_id":"oidc-oauth/state-csrf","status":"covered","note":"ok"}`, nil)
	// Unknown item rejected.
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/coverage", `{"item_id":"nope/x","status":"covered"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("set unknown item = %d, want 400", code)
	}

	// The view reflects adoption + statuses + roll-up (2 of 4 covered = 50%).
	var view methodology.View
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/methodology", &view)
	if len(view.Packs) != 1 || len(view.Packs[0].Items) != 4 {
		t.Fatalf("view packs wrong: %+v", view.Packs)
	}
	if view.Summary.Covered != 2 || view.Summary.CoveredPct != 50 {
		t.Fatalf("summary wrong: %+v", view.Summary)
	}
}
