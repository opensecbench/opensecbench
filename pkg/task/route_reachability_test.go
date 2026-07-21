package task

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// A SAST finding whose dataflow trace passes through a route handler is route_reachable — a proven
// call-graph path from an HTTP entry point to the sink, even when the sink lives in a different file.
func TestCorrelateRouteReachableViaDataflowPath(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	// A confirmed-exposed route whose handler is app/views.py.
	if err := db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "POST", Path: "/login", HandlerFile: "app/views.py", HandlerLine: 10, Observed: true}); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{code: 0})
	defer eng.Close()

	// Sink is deep in a helper, but the dataflow path starts in the route handler.
	o := &model.Observation{
		Location:   "app/db/helpers.py:99",
		Attributes: map[string]string{"dataflow_path": "app/views.py:14,app/db/helpers.py:99"},
	}
	eng.correlateExposedRoute(ctx, proj.ID, "", o)
	if o.Attributes["route_reachable"] != "true" {
		t.Fatalf("expected route_reachable=true (handler on dataflow path); attrs=%v", o.Attributes)
	}
	if o.Attributes["exposed_route"] != "POST /login" {
		t.Fatalf("exposed_route = %q, want POST /login", o.Attributes["exposed_route"])
	}

	// A path that never touches a route handler → no route_reachable (and no route attribution).
	o2 := &model.Observation{
		Location:   "app/db/helpers.py:99",
		Attributes: map[string]string{"dataflow_path": "app/db/helpers.py:1,app/db/helpers.py:99"},
	}
	eng.correlateExposedRoute(ctx, proj.ID, "", o2)
	if o2.Attributes["route_reachable"] == "true" {
		t.Fatal("route_reachable should be unset when no route handler is on the dataflow path")
	}
	if o2.Attributes["exposed_route"] != "" {
		t.Fatalf("exposed_route should be unset; got %q", o2.Attributes["exposed_route"])
	}
}
