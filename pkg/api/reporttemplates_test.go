package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// TestReportTemplateLifecycle exercises the full editor path: fork a built-in, save, preview, generate a
// report from the saved template, edit it, and delete it — plus the immutability guards on built-ins.
func TestReportTemplateLifecycle(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	// Seed a project with an evidence-backed finding so a generated report has content.
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Storefront")
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: "abc", Size: 1, Kind: model.ArtifactInput, Name: "r"})
	obs, _ := db.CreateObservation(ctx, model.Observation{
		ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "SQLi", Location: "login.go:42", Severity: "high",
	})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)
	_, _ = db.CreateFinding(ctx, store.NewFinding{
		ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high", ObservationIDs: []string{obs.ID},
	})

	// Built-ins list as read-only.
	var list []templateInfo
	postGet(t, srv.URL+"/v1/report-templates", &list)
	for _, ti := range list {
		if !ti.Builtin {
			t.Fatalf("built-in %s flagged editable", ti.ID)
		}
	}

	// Fetching a built-in returns its editable source for forking.
	var builtin reportTemplateDetail
	postGet(t, srv.URL+"/v1/report-templates/executive", &builtin)
	if !builtin.Builtin || builtin.MD == "" || builtin.HTML == "" {
		t.Fatalf("built-in source not returned: %+v", builtin)
	}

	// Create a custom template carrying a unique marker so we can see it in rendered output.
	const marker = "CUSTOM-MARKER-9137"
	body := `{"title":"Client Brief","base":"executive",` +
		`"md":"# {{.Project.Name}} ` + marker + `","html":"<!doctype html><h1>{{.Project.Name}} ` + marker + `</h1>"}`
	var created reportTemplateDetail
	if code := postJSON(t, srv.URL+"/v1/report-templates", body, &created); code != http.StatusCreated {
		t.Fatalf("create template = %d", code)
	}
	if created.ID != "client-brief" || created.Builtin {
		t.Fatalf("unexpected created template: %+v", created)
	}

	// It now lists as an editable (non-builtin) template.
	postGet(t, srv.URL+"/v1/report-templates", &list)
	if !hasTemplate(list, "client-brief", false) {
		t.Fatalf("client-brief not listed as editable: %+v", list)
	}

	// Preview renders the draft against the project's real data without saving.
	preview := getBodyPOST(t, srv.URL+"/v1/report-templates/preview",
		`{"project_id":"`+proj.ID+`","format":"html","md":"# draft","html":"<h1>{{.Project.Name}} PREVIEW</h1>"}`)
	if !strings.Contains(preview, "Acme PREVIEW") {
		t.Fatalf("preview missing rendered project name: %q", preview)
	}

	// Generate a report FROM the saved template.
	var rep model.Report
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/reports",
		`{"template":"client-brief","format":"html"}`, &rep); code != http.StatusCreated {
		t.Fatalf("generate from custom template = %d", code)
	}
	if out := getBody(t, srv.URL+"/v1/artifacts/"+rep.ArtifactID+"/content"); !strings.Contains(out, marker) {
		t.Fatalf("generated report missing custom marker; got: %q", out)
	}

	// A built-in id can't be shadowed on create.
	if code := postJSON(t, srv.URL+"/v1/report-templates",
		`{"id":"executive","title":"nope","md":"x","html":"<p>x</p>"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("create with built-in id = %d, want 400", code)
	}

	// Editing a built-in is refused (fork instead).
	if code := putJSON(t, srv.URL+"/v1/report-templates/executive",
		`{"title":"hacked","md":"x","html":"<p>x</p>"}`, nil); code != http.StatusNotFound {
		t.Fatalf("edit built-in = %d, want 404", code)
	}

	// Invalid template syntax is rejected on save.
	if code := putJSON(t, srv.URL+"/v1/report-templates/client-brief",
		`{"title":"Client Brief","md":"{{.Broken","html":"<p>ok</p>"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("edit with bad template = %d, want 400", code)
	}

	// Edit the saved template in place.
	if code := putJSON(t, srv.URL+"/v1/report-templates/client-brief",
		`{"title":"Client Brief v2","md":"# {{.Project.Name}} EDITED","html":"<h1>{{.Project.Name}} EDITED</h1>"}`, nil); code != http.StatusOK {
		t.Fatalf("edit saved template = %d, want 200", code)
	}
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/reports",
		`{"template":"client-brief","format":"html"}`, &rep); code != http.StatusCreated {
		t.Fatalf("regenerate = %d", code)
	}
	if out := getBody(t, srv.URL+"/v1/artifacts/"+rep.ArtifactID+"/content"); !strings.Contains(out, "Acme EDITED") {
		t.Fatalf("regenerated report missing edited content; got: %q", out)
	}

	// Delete unregisters it — generation with it then fails.
	if code := delReq(t, srv.URL+"/v1/report-templates/client-brief"); code != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", code)
	}
	postGet(t, srv.URL+"/v1/report-templates", &list)
	if hasTemplate(list, "client-brief", false) {
		t.Fatalf("deleted template still listed")
	}
	if code := postJSON(t, srv.URL+"/v1/projects/"+proj.ID+"/reports",
		`{"template":"client-brief","format":"html"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("generate deleted template = %d, want 400 (unknown template)", code)
	}
}

func hasTemplate(list []templateInfo, id string, builtin bool) bool {
	for _, ti := range list {
		if ti.ID == id {
			return ti.Builtin == builtin
		}
	}
	return false
}

func getBodyPOST(t *testing.T, url, body string) string {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	buf := new(strings.Builder)
	if _, err := io.Copy(buf, resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		t.Fatalf("POST %s = %d: %s", url, resp.StatusCode, buf.String())
	}
	return buf.String()
}

func delReq(t *testing.T, url string) int {
	t.Helper()
	req, _ := http.NewRequest(http.MethodDelete, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
