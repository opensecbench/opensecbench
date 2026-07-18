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

func TestGenerateReportEndToEnd(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: db, CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })
	ctx := context.Background()

	// Seed a project → app → confirmed observation → finding backed by it.
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Storefront")
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: "abc", Size: 1, Kind: model.ArtifactInput, Name: "resp"})
	obs, _ := db.CreateObservation(ctx, model.Observation{
		ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "SQLi in login", Location: "login.go:42", Severity: "high",
	})
	if err := db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateFinding(ctx, store.NewFinding{
		ApplicationID: &app.ID, Title: "Authentication bypass", Severity: "high", CWE: "CWE-287",
		ObservationIDs: []string{obs.ID},
	}); err != nil {
		t.Fatal(err)
	}

	// Templates endpoint lists the built-ins.
	var tmpls []templateInfo
	postGet(t, srv.URL+"/v1/report-templates", &tmpls)
	if len(tmpls) < 2 {
		t.Fatalf("templates = %d, want >=2", len(tmpls))
	}

	// Generate a technical HTML report.
	var rep model.Report
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/reports",
		`{"template":"technical","format":"html"}`, &rep); code != http.StatusCreated {
		t.Fatalf("generate = %d", code)
	}
	if rep.ArtifactID == "" || rep.TemplateID != "technical" || rep.Format != "html" {
		t.Fatalf("unexpected report: %+v", rep)
	}

	// The rendered bytes are downloadable and contain the finding + its evidence.
	body := getBody(t, srv.URL+"/v1/artifacts/"+rep.ArtifactID+"/content")
	for _, want := range []string{"<!doctype html>", "Authentication bypass", "CWE-287", "login.go:42"} {
		if !strings.Contains(body, want) {
			t.Fatalf("report missing %q", want)
		}
	}

	// It is listed for the project.
	var reps []model.Report
	postGet(t, srv.URL+"/v1/projects/"+proj.ID+"/reports", &reps)
	if len(reps) != 1 {
		t.Fatalf("reports listed = %d, want 1", len(reps))
	}
}
