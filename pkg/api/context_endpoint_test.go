package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// TestContextUpdateDeleteEndpoint drives the real Handler() (routing + CORS) for the context viewer's
// edit/delete. It uses PUT — PATCH is absent from the CORS allow-methods list and the browser preflight
// would reject it (the same reason the asset-update endpoint uses PUT); this test guards that contract.
func TestContextUpdateDeleteEndpoint(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng"})

	// Create a note through the real ingest endpoint so its bytes land in the CAS the handler uses.
	do := func(method, url, ctype string, body io.Reader) *http.Response {
		req, _ := http.NewRequest(method, url, body)
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		req.Header.Set("X-Project-Id", proj.ID) // the frontend sends this on every request
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	resp := do(http.MethodPost, srv.URL+"/v1/projects/"+proj.ID+"/context?name=scratch&type=note", "text/plain", strings.NewReader("first body"))
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("ingest status = %d, want 201", resp.StatusCode)
	}
	var ci model.ContextItem
	_ = json.NewDecoder(resp.Body).Decode(&ci)
	_ = resp.Body.Close()
	origArtifact := ci.ArtifactID

	// Edit: rename, tag, pin, and rewrite the body — the note repoints at a fresh artifact.
	patch, _ := json.Marshal(map[string]any{"name": "renamed", "tags": []string{"priority"}, "pinned": true, "body": "second body"})
	resp = do(http.MethodPut, srv.URL+"/v1/context/"+ci.ID, "application/json", bytes.NewReader(patch))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d, want 200", resp.StatusCode)
	}
	var upd model.ContextItem
	_ = json.NewDecoder(resp.Body).Decode(&upd)
	_ = resp.Body.Close()
	if upd.Name != "renamed" || !upd.Pinned || len(upd.Tags) != 1 || upd.Tags[0] != "priority" {
		t.Fatalf("update did not persist metadata: %#v", upd)
	}
	if upd.ArtifactID == origArtifact {
		t.Fatal("body edit did not repoint artifact_id")
	}

	// The new artifact holds the edited text.
	resp = do(http.MethodGet, srv.URL+"/v1/artifacts/"+upd.ArtifactID+"/content", "", nil)
	got, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(got) != "second body" {
		t.Fatalf("edited note content = %q, want %q", got, "second body")
	}

	// Delete: 204, then it's gone from the project's list.
	resp = do(http.MethodDelete, srv.URL+"/v1/context/"+ci.ID, "", nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}
	_ = resp.Body.Close()
	if items, _ := db.ListContextItemsByProject(ctx, proj.ID); len(items) != 0 {
		t.Fatalf("after delete, list has %d items, want 0", len(items))
	}
}
