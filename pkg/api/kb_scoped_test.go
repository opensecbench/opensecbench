package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// The scope-aware create endpoint (POST /v1/kb) can author org- and global-scoped knowledge, and a
// project under that org inherits it (ADR-0041). Also covers edit via PUT /v1/kb/{id}.
func TestKBScopedCreateAndEditAPI(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	org, _ := db.CreateOrganization(ctx, "Acme")
	target, _ := db.CreateTarget(ctx, "Platform", "", &org.ID)
	orgID := org.ID
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng", OrganizationID: &orgID, TargetIDs: []string{target.ID}})

	// Org-scoped human entry.
	var org1 model.KBEntry
	if code := postJSON(t, srv.URL+"/v1/kb", `{"scope":"org","organization_id":"`+org.ID+`","kind":"convention","title":"Org-wide logging"}`, &org1); code != http.StatusCreated {
		t.Fatalf("create org kb = %d", code)
	}
	if org1.Scope != model.KBScopeOrg || org1.OrganizationID != org.ID {
		t.Fatalf("bad org entry %+v", org1)
	}
	// Global entry.
	var glob model.KBEntry
	if code := postJSON(t, srv.URL+"/v1/kb", `{"scope":"global","kind":"tactic","title":"Everywhere"}`, &glob); code != http.StatusCreated {
		t.Fatalf("create global kb = %d", code)
	}

	// The project inherits both (org + global) plus anything target-scoped.
	var inherited []model.KBEntry
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/kb", &inherited)
	titles := map[string]bool{}
	for _, e := range inherited {
		titles[e.Title] = true
	}
	if !titles["Org-wide logging"] || !titles["Everywhere"] {
		t.Fatalf("project should inherit org + global entries, got %+v", inherited)
	}

	// Edit the org entry's body.
	var edited model.KBEntry
	if code := putJSON(t, srv.URL+"/v1/kb/"+org1.ID, `{"title":"Org-wide logging","body":"use structured JSON"}`, &edited); code != http.StatusOK {
		t.Fatalf("edit kb = %d", code)
	}
	if edited.Body != "use structured JSON" {
		t.Fatalf("edit did not persist: %+v", edited)
	}
}
