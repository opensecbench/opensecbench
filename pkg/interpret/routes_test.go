package interpret

import "testing"

// Real semgrep-OSS --json output (verified against semgrep 1.104.0 with the bundled ruleset): metavars are
// masked, so the route path is the quoted literal interpolated into extra.message; check_id is `rules.`-
// prefixed; paths carry the /src mount prefix. See docs/adr/adr-0033 + the routes.yml ruleset.
const routeJSON = `{
  "results": [
    {
      "check_id": "rules.osb-route-express-get",
      "path": "/src/server.js",
      "start": {"line": 2},
      "extra": {
        "message": "Express GET '/products/:id'",
        "metadata": {"osb_route": true, "framework": "express", "method": "GET"}
      }
    },
    {
      "check_id": "rules.osb-route-flask",
      "path": "/src/app.py",
      "start": {"line": 8},
      "extra": {
        "message": "Flask route \"/users/<int:id>\"",
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
	if get.Method != "GET" || get.Path != "/products/:id" { // method upper-cased, single quotes stripped
		t.Fatalf("express route = %+v", get)
	}
	if get.HandlerFile != "/src/server.js" || get.HandlerLine != 2 || get.Framework != "express" {
		t.Fatalf("express route location/framework = %+v", get)
	}
	if get.Source != "route-map" {
		t.Fatalf("source = %q", get.Source)
	}

	flask := routes[1]
	if flask.Path != "/users/<int:id>" || flask.Method != "" { // no method metadata → any; double quotes stripped
		t.Fatalf("flask route = %+v", flask)
	}
}

func TestRoutesEmpty(t *testing.T) {
	// A result whose message has no quoted route literal produces no route.
	routes, err := Routes([]byte(`{"results":[{"check_id":"x","path":"/src/a.py","start":{"line":1},"extra":{"message":"no route here","metadata":{}}}]}`))
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
