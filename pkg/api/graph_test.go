package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
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
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	for _, u := range []string{"https://api.acme.com/v2/users", "https://api.acme.com/v2/login", "https://cdn.acme.com/app.js"} {
		ex, _ := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: model.ExchangeProxy, Method: "GET", URL: u})
		_ = db.RecordResponse(ctx, ex.ID, 200, "", "", 10, "")
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

func TestTopologyGraph(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	// A network task associated to the project directly (no application), with nmap observations.
	pid := proj.ID
	task, _ := db.CreateTask(ctx, store.NewTask{CapabilityID: "nmap", ProjectID: &pid, Actor: "human", Runner: "fake"})
	for _, loc := range []string{"10.0.0.5:443/tcp", "10.0.0.5:22/tcp"} {
		_, _ = db.CreateObservation(ctx, model.Observation{TaskID: &task.ID, Origin: model.OriginTool, Title: "open", RuleID: "nmap/open-port", Location: loc})
	}

	var g graphResp
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/graph?kind=topology", &g)
	hosts, ports := 0, 0
	for _, n := range g.Nodes {
		if n.Kind == "host" {
			hosts++
		}
		if n.Kind == "endpoint" {
			ports++
		}
	}
	if hosts != 1 || ports != 2 || len(g.Edges) != 2 {
		t.Fatalf("topology graph wrong: hosts=%d ports=%d edges=%d", hosts, ports, len(g.Edges))
	}
}

func TestDependencyGraph(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	pid := proj.ID
	// A succeeded syft task with a CycloneDX SBOM output artifact in the CAS.
	sbom := `{"components":[{"bom-ref":"pkg:a","name":"a","version":"1.0"},{"bom-ref":"pkg:b","name":"b","version":"2.0"}],
	          "dependencies":[{"ref":"pkg:a","dependsOn":["pkg:b"]}]}`
	digest, _ := blobs.Put(strings.NewReader(sbom))
	task, _ := db.CreateTask(ctx, store.NewTask{CapabilityID: "syft", ProjectID: &pid, Actor: "human", Runner: "fake"})
	code := 0
	_ = db.FinishTask(ctx, task.ID, model.TaskSucceeded, &code, "")
	_, _ = db.CreateArtifact(ctx, model.Artifact{TaskID: &task.ID, SHA256: digest, Size: int64(len(sbom)), Kind: model.ArtifactOutput, Name: "sbom.cdx.json"})

	var g graphResp
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/graph?kind=dependency", &g)
	if len(g.Nodes) != 2 || len(g.Edges) != 1 {
		t.Fatalf("dependency graph wrong: %d nodes / %d edges: %+v", len(g.Nodes), len(g.Edges), g)
	}
}
