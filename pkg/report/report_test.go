package report

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var errNotFound = errors.New("not found")

// fakeSource is an in-memory Source for testing the Builder without a database.
type fakeSource struct {
	project  model.Project
	apps     []model.Application
	assets   map[string][]model.Asset
	findings []model.Finding
	obs      map[string]model.Observation
	scope    []model.ScopeEntry
	tasks    []model.Task
	adopted  []string
	coverage []model.CoverageEntry
}

func (f *fakeSource) ListAdoptedMethodologies(context.Context, string) ([]string, error) {
	return f.adopted, nil
}
func (f *fakeSource) ListCoverage(context.Context, string) ([]model.CoverageEntry, error) {
	return f.coverage, nil
}

func (f *fakeSource) GetProject(context.Context, string) (model.Project, error) {
	return f.project, nil
}
func (f *fakeSource) ListApplicationsByProject(context.Context, string) ([]model.Application, error) {
	return f.apps, nil
}
func (f *fakeSource) ListAssetsByApplication(_ context.Context, id string) ([]model.Asset, error) {
	return f.assets[id], nil
}
func (f *fakeSource) ListFindings(context.Context) ([]model.Finding, error) { return f.findings, nil }
func (f *fakeSource) GetFinding(_ context.Context, id string) (model.Finding, error) {
	for _, x := range f.findings {
		if x.ID == id {
			return x, nil
		}
	}
	return model.Finding{}, errNotFound
}
func (f *fakeSource) GetObservation(_ context.Context, id string) (model.Observation, error) {
	o, ok := f.obs[id]
	if !ok {
		return model.Observation{}, errNotFound
	}
	return o, nil
}
func (f *fakeSource) ListScopeEntries(context.Context, string) ([]model.ScopeEntry, error) {
	return f.scope, nil
}
func (f *fakeSource) ListTasks(context.Context, int) ([]model.Task, error) { return f.tasks, nil }

func appPtr(s string) *string { return &s }

func sampleSource() *fakeSource {
	return &fakeSource{
		project: model.Project{ID: "p1", Name: "Acme Web", Status: "active"},
		apps:    []model.Application{{ID: "a1", Name: "Storefront"}},
		assets:  map[string][]model.Asset{"a1": {{ID: "as1"}, {ID: "as2"}}},
		scope:   []model.ScopeEntry{{Kind: "domain", Value: "acme.com"}},
		tasks: []model.Task{
			{ID: "t1", CapabilityID: "semgrep", ApplicationID: appPtr("a1")},
			{ID: "t2", CapabilityID: "semgrep", ApplicationID: appPtr("a1")},
			{ID: "t3", CapabilityID: "http-probe", ApplicationID: appPtr("a1")},
		},
		obs: map[string]model.Observation{
			"o1": {ID: "o1", Title: "SQL injection in /login", Location: "login.go:42", Origin: "tool", ReviewState: "confirmed"},
		},
		findings: []model.Finding{
			{ID: "f1", ApplicationID: appPtr("a1"), Title: "Auth bypass", Severity: "high", Status: "confirmed", CWE: "CWE-89", ObservationIDs: []string{"o1"}},
			{ID: "f2", ApplicationID: appPtr("a1"), Title: "Critical RCE", Severity: "critical", Status: "open", ObservationIDs: []string{"o1"}},
			{ID: "f3", ApplicationID: appPtr("a1"), Title: "No evidence", Severity: "low", Status: "open", ObservationIDs: []string{}},
			{ID: "f4", ApplicationID: appPtr("a1"), Title: "Dismissed", Severity: "high", Status: "false_positive", ObservationIDs: []string{"o1"}},
			{ID: "f5", ApplicationID: appPtr("other"), Title: "Other project", Severity: "high", Status: "open", ObservationIDs: []string{"o1"}},
		},
	}
}

func TestBuildAppliesEvidenceRuleAndOrder(t *testing.T) {
	d, err := NewBuilder(sampleSource()).Build(context.Background(), "p1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// f3 (no evidence), f4 (false positive), f5 (other project) excluded → only f1, f2.
	if d.Summary.Total != 2 || len(d.Findings) != 2 {
		t.Fatalf("reportable = %d, want 2: %+v", d.Summary.Total, titles(d.Findings))
	}
	// Ordered most-severe first: critical before high.
	if d.Findings[0].Title != "Critical RCE" || d.Findings[1].Title != "Auth bypass" {
		t.Fatalf("order wrong: %v", titles(d.Findings))
	}
	if d.Summary.BySeverity["critical"] != 1 || d.Summary.BySeverity["high"] != 1 {
		t.Fatalf("severity counts wrong: %+v", d.Summary.BySeverity)
	}
	// Coverage: 3 tasks, 2 distinct capabilities, 2 assets.
	if d.Summary.TasksRun != 3 || len(d.Summary.Capabilities) != 2 || d.Summary.Assets != 2 {
		t.Fatalf("coverage wrong: %+v", d.Summary)
	}
	// Evidence attached.
	if len(d.Findings[0].Evidence) != 1 || d.Findings[0].Evidence[0].Location != "login.go:42" {
		t.Fatalf("evidence not attached: %+v", d.Findings[0].Evidence)
	}
}

func TestRenderTemplates(t *testing.T) {
	d, _ := NewBuilder(sampleSource()).Build(context.Background(), "p1", time.Now())
	reg := BuiltIns()

	for _, id := range []string{"executive", "technical"} {
		tmpl, ok := reg.Get(id)
		if !ok {
			t.Fatalf("template %s missing", id)
		}
		md, err := tmpl.Render(d, FormatMarkdown)
		if err != nil {
			t.Fatalf("%s md: %v", id, err)
		}
		html, err := tmpl.Render(d, FormatHTML)
		if err != nil {
			t.Fatalf("%s html: %v", id, err)
		}
		if !strings.Contains(string(md), "Acme Web") || !strings.Contains(string(md), "Critical RCE") {
			t.Fatalf("%s md missing content:\n%s", id, md)
		}
		if !strings.Contains(string(html), "<!doctype html>") || !strings.Contains(string(html), "Critical RCE") {
			t.Fatalf("%s html missing content", id)
		}
		// The inline-SVG severity figure is embedded (not escaped) in HTML reports.
		if !strings.Contains(string(html), "<svg") || strings.Contains(string(html), "&lt;svg") {
			t.Fatalf("%s html missing inline severity chart", id)
		}
	}

	// Technical report shows evidence + scope; executive does not need the evidence detail.
	tech, _ := reg.Get("technical")
	out, _ := tech.Render(d, FormatMarkdown)
	if !strings.Contains(string(out), "login.go:42") || !strings.Contains(string(out), "acme.com") {
		t.Fatalf("technical report missing evidence/scope:\n%s", out)
	}
}

func TestRetestGroupsByStatus(t *testing.T) {
	src := sampleSource()
	// f1 confirmed (still open), f2 open (still open); add a remediated + accepted one.
	src.findings = append(src.findings,
		model.Finding{ID: "f6", ApplicationID: appPtr("a1"), Title: "Fixed XSS", Severity: "medium", Status: "remediated", ObservationIDs: []string{"o1"}},
		model.Finding{ID: "f7", ApplicationID: appPtr("a1"), Title: "Accepted CSRF", Severity: "low", Status: "accepted", ObservationIDs: []string{"o1"}},
	)
	d, _ := NewBuilder(src).Build(context.Background(), "p1", time.Now())
	retest, ok := BuiltIns().Get("retest")
	if !ok {
		t.Fatal("retest template missing")
	}
	out, err := retest.Render(d, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "Remediated:** 1") || !strings.Contains(s, "Still open:** 2") || !strings.Contains(s, "Accepted risk:** 1") {
		t.Fatalf("retest counts wrong:\n%s", s)
	}
	if !strings.Contains(s, "Fixed XSS") || !strings.Contains(s, "Accepted CSRF") {
		t.Fatalf("retest missing grouped findings:\n%s", s)
	}
}

func TestComplianceGroupsByCWE(t *testing.T) {
	src := sampleSource()
	// f1 has CWE-89, f2 has none → "Unmapped".
	d, _ := NewBuilder(src).Build(context.Background(), "p1", time.Now())
	groups := CWEGroups(d.Findings)
	// Most-severe group first (f2 critical is Unmapped, f1 high is CWE-89) → Unmapped sorts LAST
	// despite severity, by rule.
	if len(groups) != 2 {
		t.Fatalf("groups = %d, want 2: %+v", len(groups), groups)
	}
	if groups[len(groups)-1].CWE != "Unmapped" {
		t.Fatalf("Unmapped should sort last: %+v", groups)
	}

	tmpl, ok := BuiltIns().Get("compliance")
	if !ok {
		t.Fatal("compliance template missing")
	}
	out, err := tmpl.Render(d, FormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "CWE-89") || !strings.Contains(string(out), "Unmapped") {
		t.Fatalf("compliance report missing CWE grouping:\n%s", out)
	}
}

func TestReportIncludesMethodologyCoverage(t *testing.T) {
	src := sampleSource()
	src.adopted = []string{"oidc-oauth"}
	src.coverage = []model.CoverageEntry{
		{ItemID: "oidc-oauth/pkce", Status: "covered"},
		{ItemID: "oidc-oauth/state-csrf", Status: "covered"},
	}
	d, _ := NewBuilder(src).Build(context.Background(), "p1", time.Now())
	if d.Methodology.Summary.Total != 4 || d.Methodology.Summary.Covered != 2 {
		t.Fatalf("methodology summary wrong: %+v", d.Methodology.Summary)
	}
	tech, _ := BuiltIns().Get("technical")
	out, _ := tech.Render(d, FormatMarkdown)
	if !strings.Contains(string(out), "Methodology coverage") || !strings.Contains(string(out), "PKCE for public clients") {
		t.Fatalf("technical report missing methodology section:\n%s", out)
	}
	exec, _ := BuiltIns().Get("executive")
	eout, _ := exec.Render(d, FormatMarkdown)
	if !strings.Contains(string(eout), "Methodology coverage: **50%**") {
		t.Fatalf("executive missing coverage line:\n%s", eout)
	}
}

func titles(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Title)
	}
	return out
}

func TestBrandedReport(t *testing.T) {
	d, _ := NewBuilder(sampleSource()).Build(context.Background(), "p1", time.Now())
	d.Brand = Brand{Name: "Acme Security", Tagline: "Confidential", Color: "#0b5"}
	tmpl, _ := BuiltIns().Get("branded")
	html, err := tmpl.Render(d, FormatHTML)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Acme Security", "Confidential", "#0b5", "Critical RCE"} {
		if !strings.Contains(string(html), want) {
			t.Fatalf("branded report missing %q", want)
		}
	}
}

func TestDOCXIsValidZip(t *testing.T) {
	d, _ := NewBuilder(sampleSource()).Build(context.Background(), "p1", time.Now())
	out, err := DOCX("Technical report", d)
	if err != nil {
		t.Fatal(err)
	}
	// Valid zip with the required parts.
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	need := map[string]bool{"[Content_Types].xml": false, "_rels/.rels": false, "word/document.xml": false}
	var docXML string
	for _, f := range zr.File {
		if _, ok := need[f.Name]; ok {
			need[f.Name] = true
		}
		if f.Name == "word/document.xml" {
			rc, _ := f.Open()
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			docXML = string(b)
		}
	}
	for name, present := range need {
		if !present {
			t.Fatalf("docx missing required part %q", name)
		}
	}
	if !strings.Contains(docXML, "Critical RCE") || !strings.Contains(docXML, "Acme Web") {
		t.Fatalf("docx body missing content")
	}
}

func TestRegistryAddExtensionTemplate(t *testing.T) {
	reg := BuiltIns()
	err := reg.Add("custom", "Custom pack report", "custom",
		"# {{.Project.Name}} custom\n{{.Summary.Total}} findings",
		"<h1>{{.Project.Name}}</h1><p>{{.Summary.Total}} findings</p>")
	if err != nil {
		t.Fatal(err)
	}
	tmpl, ok := reg.Get("custom")
	if !ok {
		t.Fatal("added template not found")
	}
	d, _ := NewBuilder(sampleSource()).Build(context.Background(), "p1", time.Now())
	out, err := tmpl.Render(d, FormatHTML)
	if err != nil || !strings.Contains(string(out), "Acme Web") {
		t.Fatalf("extension template render failed: %v", err)
	}
	// A bad template string returns a parse error, not a panic.
	if err := reg.Add("bad", "Bad", "x", "{{.Nope", "{{.Nope"); err == nil {
		t.Fatal("expected parse error for malformed template")
	}
}
