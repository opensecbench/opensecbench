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

// The summary rollup aggregates findings by severity, reachable count, routes, and dependency signals.
func TestProjectSummary(t *testing.T) {
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
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	appID := app.ID

	if _, err := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &appID, Title: "SQLi", Severity: "high"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &appID, Title: "XSS", Severity: "medium"}); err != nil {
		t.Fatal(err)
	}
	// A reachable vuln observation and an outdated one.
	if _, err := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Origin: model.OriginTool, Title: "CVE-2022-1", Severity: "high", RuleID: "CVE-2022-0001", Attributes: map[string]string{"reachable": "true"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Origin: model.OriginTool, Title: "outdated flask", Severity: "low", RuleID: "outdated/PYPI", Attributes: map[string]string{"outdated": "true", "package": "flask"}}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "POST", Path: "/login", Observed: true}); err != nil {
		t.Fatal(err)
	}

	var got projectSummary
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/summary", &got)

	if got.Findings["total"] != 2 || got.Findings["high"] != 1 || got.Findings["medium"] != 1 {
		t.Fatalf("findings = %+v, want total 2 (1 high, 1 medium)", got.Findings)
	}
	if got.Reachable != 1 {
		t.Fatalf("reachable = %d, want 1", got.Reachable)
	}
	if got.Dependencies.Vulnerabilities != 1 || got.Dependencies.Outdated != 1 {
		t.Fatalf("deps = %+v, want 1 vulnerable + 1 outdated", got.Dependencies)
	}
	if got.Routes.Total != 1 || got.Routes.Exposed != 1 {
		t.Fatalf("routes = %+v, want 1 total / 1 exposed", got.Routes)
	}
}
