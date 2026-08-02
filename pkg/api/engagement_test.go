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

func TestCreateProjectWithEngagement(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })

	// Create a project with its engagement record + scope (allow + deny) in one call.
	body := `{
		"name": "Acme Q3",
		"engagement": {"kinds":["web","api"], "objective":"pre-launch review", "environment":"staging",
		               "data_class":"restricted", "authorized":true, "authorizer":"j@acme.com",
		               "techniques":{"intrusive":true,"dos":false}},
		"scope": [{"kind":"domain","value":"acme.com","disposition":"allow"},
		          {"kind":"host","value":"payments.acme.com","disposition":"deny"}]
	}`
	var proj model.Project
	if code := postJSON(t, srv.URL+"/v1/projects", body, &proj); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}

	// Engagement round-trips.
	var eng model.Engagement
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/engagement", &eng)
	if eng.DataClass != model.DataRestricted || !eng.Authorized || len(eng.Kinds) != 2 || !eng.Techniques["intrusive"] {
		t.Fatalf("engagement not saved: %+v", eng)
	}

	// Scope has the deny entry.
	var scope []model.ScopeEntry
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/scope", &scope)
	var deny int
	for _, e := range scope {
		if e.Disposition == model.ScopeDeny {
			deny++
		}
	}
	if len(scope) != 2 || deny != 1 {
		t.Fatalf("scope wrong: %+v", scope)
	}

	// PUT updates the record.
	var updated model.Engagement
	if code := putJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/engagement", `{"objective":"revised"}`, &updated); code != http.StatusOK {
		t.Fatalf("put = %d", code)
	}
	if updated.Objective != "revised" {
		t.Fatalf("update not applied: %+v", updated)
	}
}

func TestAssetLocationResolvesAgainstBasePath(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })

	var proj model.Project
	postJSON(t, srv.URL+"/v1/projects", `{"name":"P","engagement":{"base_path":"/work/acme"}}`, &proj)
	var app model.Application
	postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/applications", `{"name":"a"}`, &app)

	// Relative location is anchored under the base path.
	var rel model.Asset
	postJSON(t, srv.URL+"/v1/applications/"+app.ID+"/assets", `{"type":"source_repo","location":"services/api","sensitivity":"private"}`, &rel)
	if rel.Location != "/work/acme/services/api" {
		t.Fatalf("relative location not anchored: %q", rel.Location)
	}
	// Absolute location and URLs pass through unchanged.
	var abs model.Asset
	postJSON(t, srv.URL+"/v1/applications/"+app.ID+"/assets", `{"type":"source_repo","location":"/opt/other","sensitivity":"private"}`, &abs)
	if abs.Location != "/opt/other" {
		t.Fatalf("absolute location should pass through: %q", abs.Location)
	}
}
