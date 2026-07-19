package interpret

import "testing"

// semgrep --json for the route ruleset: an Express GET route and a Flask route (no method metadata).
const routeJSON = `{
  "results": [
    {
      "check_id": "osb-route-express-get",
      "path": "src/routes/users.js",
      "start": {"line": 20},
      "extra": {
        "metavars": {"$ROUTE": {"abstract_content": "'/users/:id'"}},
        "metadata": {"osb_route": true, "framework": "express", "method": "get"}
      }
    },
    {
      "check_id": "osb-route-flask",
      "path": "app/views.py",
      "start": {"line": 8},
      "extra": {
        "metavars": {"$ROUTE": {"abstract_content": "\"/health\""}},
        "metadata": {"osb_route": true, "framework": "flask"}
      }
    }
  ]
}`

func TestRoutes(t *testing.T) {
	routes, err := Routes([]byte(routeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 2 {
		t.Fatalf("got %d routes, want 2", len(routes))
	}

	get := routes[0]
	if get.Method != "GET" || get.Path != "/users/:id" { // method upper-cased, quotes stripped
		t.Fatalf("express route = %+v", get)
	}
	if get.HandlerFile != "src/routes/users.js" || get.HandlerLine != 20 || get.Framework != "express" {
		t.Fatalf("express route location/framework = %+v", get)
	}
	if get.Source != "route-map" {
		t.Fatalf("source = %q", get.Source)
	}

	flask := routes[1]
	if flask.Path != "/health" || flask.Method != "" { // no method metadata → any
		t.Fatalf("flask route = %+v", flask)
	}
}

func TestRoutesEmpty(t *testing.T) {
	// A result with no $ROUTE metavar produces no route.
	routes, err := Routes([]byte(`{"results":[{"check_id":"x","path":"a.py","start":{"line":1},"extra":{"metavars":{},"metadata":{}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(routes) != 0 {
		t.Fatalf("want 0 routes, got %d", len(routes))
	}
}

func TestRoutesBadJSON(t *testing.T) {
	if _, err := Routes([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
