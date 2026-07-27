package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/methodology"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// httpDo runs an arbitrary-method JSON request and returns the status and raw body.
func httpDo(t *testing.T, method, url, body string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestMethodologyAuthoringCRUD(t *testing.T) {
	srv := newTestServer(t)

	// Create a user pack — ids and defaults are derived server-side.
	var created methodology.Methodology
	if code := postJSON(t, srv.URL+"/v1/methodologies",
		`{"title":"GraphQL API","keywords":["graphql","gql"],"items":[{"title":"Query depth limiting","objective":"Bound query cost"}]}`,
		&created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}
	if created.ID != "graphql-api" || created.Tech != "custom" || created.Version != "1.0.0" {
		t.Fatalf("derived fields wrong: %+v", created)
	}
	if len(created.Items) != 1 || created.Items[0].ID != "graphql-api/query-depth-limiting" {
		t.Fatalf("item id not derived: %+v", created.Items)
	}

	// It's registered: appears in the catalog flagged as editable, and its item is adoptable + coverable.
	var catalog []methodology.Methodology
	postGet(t, srv.URL+"/v1/methodologies", &catalog)
	var saw, sawBuiltin bool
	for _, p := range catalog {
		if p.ID == "graphql-api" {
			saw = true
			if p.Builtin {
				t.Fatal("user pack flagged builtin")
			}
		}
		if p.ID == "web-app" && p.Builtin {
			sawBuiltin = true
		}
	}
	if !saw || !sawBuiltin {
		t.Fatalf("catalog flags wrong: saw=%v sawBuiltin=%v", saw, sawBuiltin)
	}

	var proj model.Project
	postJSON(t, srv.URL+"/v1/projects", `{"name":"gql engagement"}`, &proj)
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/methodology/adopt", `{"methodology_id":"graphql-api"}`, nil); code != http.StatusNoContent {
		t.Fatalf("adopt user pack = %d", code)
	}
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/coverage", `{"item_id":"graphql-api/query-depth-limiting","status":"covered"}`, nil); code != http.StatusNoContent {
		t.Fatalf("cover user item = %d", code)
	}

	// A built-in can't be edited or deleted.
	if code, _ := httpDo(t, http.MethodPut, srv.URL+"/v1/methodologies/web-app", `{"title":"hijack","items":[{"title":"x"}]}`); code != http.StatusNotFound {
		t.Fatalf("edit built-in = %d, want 404", code)
	}
	if code, _ := httpDo(t, http.MethodDelete, srv.URL+"/v1/methodologies/web-app", ``); code != http.StatusNotFound {
		t.Fatalf("delete built-in = %d, want 404", code)
	}

	// Duplicate id (a second pack whose title slugs to graphql-api) is rejected.
	if code := postJSON(t, srv.URL+"/v1/methodologies", `{"title":"GraphQL API","items":[{"title":"y"}]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("duplicate create = %d, want 400", code)
	}

	// Update in place.
	var updated methodology.Methodology
	code, body := httpDo(t, http.MethodPut, srv.URL+"/v1/methodologies/graphql-api",
		`{"title":"GraphQL API v2","items":[{"id":"graphql-api/query-depth-limiting","title":"Query depth"},{"title":"Introspection disabled"}]}`)
	if code != http.StatusOK {
		t.Fatalf("update = %d (%s)", code, body)
	}
	_ = json.Unmarshal(body, &updated)
	if updated.Title != "GraphQL API v2" || len(updated.Items) != 2 {
		t.Fatalf("update not applied: %+v", updated)
	}

	// Delete removes it from the catalog AND sweeps the project's orphaned adoption + coverage.
	if code, _ := httpDo(t, http.MethodDelete, srv.URL+"/v1/methodologies/graphql-api", ``); code != http.StatusNoContent {
		t.Fatalf("delete = %d", code)
	}
	var after []methodology.Methodology
	postGet(t, srv.URL+"/v1/methodologies", &after)
	for _, p := range after {
		if p.ID == "graphql-api" {
			t.Fatal("deleted pack still in catalog")
		}
	}
	// The project no longer shows the pack as adopted, and its coverage row is gone (not orphaned).
	var view methodology.View
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/methodology", &view)
	for _, p := range view.Packs {
		if p.ID == "graphql-api" {
			t.Fatalf("deleted pack still adopted after sweep: %+v", view.Packs)
		}
	}
}
