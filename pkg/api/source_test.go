package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/srcfile"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

func TestSourceViewerEndpoints(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	// A real on-disk repo to point the asset at.
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "app", "views.py"), []byte("def a():\n    pass\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Store")
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo, Sensitivity: "private"})

	// Read a file.
	var file srcfile.File
	postGet(t, srv.URL+"/v1/assets/"+asset.ID+"/source?path=app/views.py", &file)
	if file.Content != "def a():\n    pass\n" || file.Lines != 3 {
		t.Fatalf("unexpected file: %+v", file)
	}

	// List the root tree — "app" dir present, noise excluded (none here).
	var tree []srcfile.Entry
	postGet(t, srv.URL+"/v1/assets/"+asset.ID+"/tree", &tree)
	if len(tree) != 1 || tree[0].Name != "app" || !tree[0].Dir {
		t.Fatalf("unexpected tree: %+v", tree)
	}

	// Path traversal must be refused.
	resp, err := http.Get(srv.URL + "/v1/assets/" + asset.ID + "/source?path=../../etc/passwd")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("traversal returned %d, want 400", resp.StatusCode)
	}

	// A missing file is a 404, not a 500.
	resp, err = http.Get(srv.URL + "/v1/assets/" + asset.ID + "/source?path=app/missing.py")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing file returned %d, want 404", resp.StatusCode)
	}
}
