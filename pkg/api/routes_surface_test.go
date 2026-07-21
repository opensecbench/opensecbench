package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// The routes surface joins the entry-point inventory with the findings reachable from each route and ranks
// the riskiest first.
func TestRoutesSurface(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "eng"})
	// A risky, traffic-confirmed route with a reachable finding; a clean one.
	if err := db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "POST", Path: "/login", HandlerFile: "app/views.py", HandlerLine: 10, Observed: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "GET", Path: "/health", HandlerFile: "app/views.py", HandlerLine: 40, Observed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Origin: model.OriginTool, Title: "SQL injection", Severity: "high",
		RuleID: "python.sql-injection", Location: "app/db.py:99",
		Attributes: map[string]string{"exposed_route": "POST /login", "route_reachable": "true"},
	}); err != nil {
		t.Fatal(err)
	}

	var got []routeView
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/routes", &got)
	if len(got) != 2 {
		t.Fatalf("got %d routes, want 2", len(got))
	}
	// The risky route ranks first.
	if got[0].Path != "/login" {
		t.Fatalf("riskiest route should sort first, got %q", got[0].Path)
	}
	if got[0].WorstSeverity != "high" || got[0].ReachableCount != 1 {
		t.Fatalf("login route = worst=%q reachable=%d, want high/1", got[0].WorstSeverity, got[0].ReachableCount)
	}
	if len(got[0].Findings) != 1 || !got[0].Findings[0].RouteReachable {
		t.Fatalf("login route should carry its reachable finding, got %+v", got[0].Findings)
	}
	if len(got[1].Findings) != 0 {
		t.Fatalf("health route should be clean, got %+v", got[1].Findings)
	}
}
