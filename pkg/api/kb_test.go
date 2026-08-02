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

func TestKBInheritanceAndReviewAPI(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	target, _ := db.CreateTarget(ctx, "Acme Platform", "", nil)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "engagement", TargetIDs: []string{target.ID}})

	// Human entry via API defaults to confirmed.
	var human model.KBEntry
	if code := postJSON(t, srv.URL+"/v1/targets/"+target.ID+"/kb", `{"kind":"auth","title":"SAML SSO via Okta"}`, &human); code != http.StatusCreated {
		t.Fatalf("create kb = %d", code)
	}
	if human.ReviewState != model.ReviewConfirmed {
		t.Fatalf("human entry should be confirmed: %+v", human)
	}

	// An agent-drafted (unreviewed) entry.
	draft, _ := db.CreateKBEntry(ctx, model.KBEntry{
		TargetID: target.ID, Kind: "endpoint", Title: "GraphQL at /api/graphql", Origin: model.OriginThread,
	})

	// The project inherits both via its target.
	var inherited []model.KBEntry
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/kb", &inherited)
	if len(inherited) != 2 {
		t.Fatalf("project inherited %d entries, want 2", len(inherited))
	}

	// Confirm the draft via API.
	var confirmed model.KBEntry
	if code := postJSON(t, srv.URL+"/v1/kb/"+draft.ID+"/review", `{"state":"confirmed"}`, &confirmed); code != http.StatusOK {
		t.Fatalf("review = %d", code)
	}
	if confirmed.ReviewState != model.ReviewConfirmed {
		t.Fatalf("draft not confirmed: %+v", confirmed)
	}

	// KB shows up in omni-search.
	var hits []model.SearchResult
	postGet(t, srv.URL+"/v1/search?q=GraphQL", &hits)
	found := false
	for _, h := range hits {
		if h.Kind == "kb" {
			found = true
		}
	}
	if !found {
		t.Fatalf("KB entry not surfaced in search: %+v", hits)
	}
}
