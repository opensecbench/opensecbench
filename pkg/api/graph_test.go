package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestProjectStructureGraph(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: db, CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Storefront")
	_, _ = db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: "source_repo", Location: "/work/repo", Sensitivity: "private"})
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: "x", Size: 1, Kind: model.ArtifactInput, Name: "e"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "o", Severity: "high"})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)
	_, _ = db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high", ObservationIDs: []string{obs.ID}})

	var g graphResp
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/graph?kind=structure", &g)
	// project + app + asset + finding = 4 nodes; 3 edges (p→a, a→as, a→f)
	if len(g.Nodes) != 4 || len(g.Edges) != 3 {
		t.Fatalf("structure graph = %d nodes / %d edges, want 4/3: %+v", len(g.Nodes), len(g.Edges), g)
	}
	kinds := map[string]int{}
	for _, n := range g.Nodes {
		kinds[n.Kind]++
	}
	if kinds["project"] != 1 || kinds["application"] != 1 || kinds["asset"] != 1 || kinds["finding"] != 1 {
		t.Fatalf("node kinds wrong: %+v", kinds)
	}
}

func TestProjectTrafficGraph(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: db, CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	for _, u := range []string{"https://api.acme.com/v2/users", "https://api.acme.com/v2/login", "https://cdn.acme.com/app.js"} {
		ex, _ := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: model.ExchangeProxy, Method: "GET", URL: u})
		_ = db.RecordResponse(ctx, ex.ID, 200, "", "", 10)
	}

	var g graphResp
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/graph?kind=traffic", &g)
	// 2 hosts + 3 endpoints = 5 nodes; 3 edges
	hosts, endpoints := 0, 0
	for _, n := range g.Nodes {
		switch n.Kind {
		case "host":
			hosts++
		case "endpoint":
			endpoints++
		}
	}
	if hosts != 2 || endpoints != 3 || len(g.Edges) != 3 {
		t.Fatalf("traffic graph wrong: hosts=%d endpoints=%d edges=%d", hosts, endpoints, len(g.Edges))
	}
	_ = http.StatusOK
}
