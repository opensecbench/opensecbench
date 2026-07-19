package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestRouteMatchesPath(t *testing.T) {
	cases := []struct {
		route, actual string
		want          bool
	}{
		{"/users", "/users", true},
		{"/users/{id}", "/users/123", true},
		{"/users/:id", "/users/123", true},
		{"/users/<int:id>", "/users/9", true},
		{"/files/*", "/files/a", true},
		{"/users/{id}", "/users/123/edit", false}, // length mismatch
		{"/users", "/accounts", false},
		{"/a/{x}/c", "/a/b/c", true},
		{"/a/{x}/c", "/a/b/d", false},
	}
	for _, c := range cases {
		if got := routeMatchesPath(c.route, c.actual); got != c.want {
			t.Errorf("routeMatchesPath(%q, %q) = %v, want %v", c.route, c.actual, got, c.want)
		}
	}
}

func TestRouteUpsertAndHandlerLookup(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, err := db.CreateProject(ctx, NewProject{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}

	if err := db.UpsertRoute(ctx, model.Route{
		ProjectID: proj.ID, Method: "GET", Path: "/users/{id}", HandlerFile: "app/views.py",
		HandlerLine: 12, Framework: "flask", Source: "route-map",
	}); err != nil {
		t.Fatal(err)
	}
	// Re-extraction updates handler metadata without clearing observed (set below via reconcile).
	if err := db.UpsertRoute(ctx, model.Route{
		ProjectID: proj.ID, Method: "GET", Path: "/users/{id}", HandlerFile: "app/views.py", HandlerLine: 15,
	}); err != nil {
		t.Fatal(err)
	}
	all, _ := db.ListRoutesByProject(ctx, proj.ID)
	if len(all) != 1 || all[0].HandlerLine != 15 {
		t.Fatalf("upsert should update in place: %+v", all)
	}

	byFile, _ := db.RoutesForHandlerFile(ctx, proj.ID, "app/views.py")
	if len(byFile) != 1 {
		t.Fatalf("want 1 route for app/views.py, got %d", len(byFile))
	}
	if none, _ := db.RoutesForHandlerFile(ctx, proj.ID, "other.py"); len(none) != 0 {
		t.Fatalf("no route in other.py, got %d", len(none))
	}
}

func TestReconcileObservedRoutes(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})

	// A declared route, not yet confirmed.
	_ = db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "GET", Path: "/users/{id}", HandlerFile: "v.py", Source: "route-map"})

	// Traffic: one request matches the declared route; one hits an undeclared endpoint.
	if _, err := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: "proxy", Method: "GET", URL: "https://app.example.com/users/42"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: "proxy", Method: "POST", URL: "https://app.example.com/login"}); err != nil {
		t.Fatal(err)
	}

	if err := db.ReconcileObservedRoutes(ctx, proj.ID); err != nil {
		t.Fatal(err)
	}

	routes, _ := db.ListRoutesByProject(ctx, proj.ID)
	byPath := map[string]model.Route{}
	for _, r := range routes {
		byPath[r.Path] = r
	}
	// The declared route is now traffic-confirmed.
	if !byPath["/users/{id}"].Observed {
		t.Fatal("declared route matched by traffic should be observed")
	}
	// The undeclared endpoint is recorded as a traffic-only route (graceful degradation with no source).
	tr, ok := byPath["/login"]
	if !ok || !tr.Observed || tr.Source != "traffic" || tr.HandlerFile != "" {
		t.Fatalf("undeclared live endpoint should be a traffic-only observed route: %+v", tr)
	}
}
