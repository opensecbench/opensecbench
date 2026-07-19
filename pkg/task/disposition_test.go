package task

import (
	"context"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/interpret"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// dispoCap is a fake capability whose output the fake TruffleHog interpreter turns into a verified + an
// unverified secret observation, and which declares the same dispositions the trufflehog extension ships.
type dispoCap struct{ dispositions []disposition.Disposition }

func (c dispoCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "fake-secrets", Version: "1.0.0", OutputName: "out.json",
		OutputMediaType: interpret.TruffleHogMediaType, OKExitCodes: []int{0},
		Dispositions: c.dispositions,
	}
}
func (dispoCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

// Two NDJSON secrets: one verified, one not — exercising both disposition branches.
const twoSecrets = `{"DetectorName":"AWS","Verified":true,"Redacted":"AKIA…","SourceMetadata":{"Data":{"Filesystem":{"file":"a.env","line":1}}}}
{"DetectorName":"Slack","Verified":false,"Redacted":"xoxb…","SourceMetadata":{"Data":{"Filesystem":{"file":"b.env","line":2}}}}`

func dispoEngine(t *testing.T) (*Engine, *store.DB) {
	t.Helper()
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(dispoCap{dispositions: []disposition.Disposition{
		{When: map[string]string{"verified": "true"}, Action: disposition.ActionFinding},
		{When: map[string]string{"verified": "false"}, Action: disposition.ActionInvestigate},
	}})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(twoSecrets), code: 0})
	t.Cleanup(func() { eng.Close() })
	return eng, db
}

func TestDispositionRoutesVerifiedAndUnverified(t *testing.T) {
	eng, db := dispoEngine(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")

	tk, err := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-secrets", TargetDir: "/repo", ApplicationID: &app.ID})
	if err != nil {
		t.Fatal(err)
	}
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	// The verified secret auto-promotes to a (confirmed) finding.
	findings, _ := db.ListFindings(ctx)
	if len(findings) != 1 || findings[0].Severity != "high" {
		t.Fatalf("findings = %+v, want 1 high (the verified secret)", findings)
	}
	// The unverified secret opens an investigation.
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(invs) != 1 || invs[0].Status != model.InvestigationOpen {
		t.Fatalf("investigations = %+v, want 1 open (the unverified secret)", invs)
	}
	// Both observations exist; the verified one is confirmed, the unverified one still unreviewed.
	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	var confirmed, unreviewed int
	for _, o := range obs {
		switch o.ReviewState {
		case model.ReviewConfirmed:
			confirmed++
		case model.ReviewUnreviewed:
			unreviewed++
		}
	}
	if confirmed != 1 || unreviewed != 1 {
		t.Fatalf("review states confirmed=%d unreviewed=%d, want 1/1", confirmed, unreviewed)
	}
}

// A re-scan that reproduces the same findings must not duplicate observations or re-fire dispositions —
// the content fingerprint dedups, so investigations (and their token cost) don't re-open every run (ADR-0029).
func TestReScanDedupsObservationsAndDispositions(t *testing.T) {
	eng, db := dispoEngine(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")

	run := func() {
		tk, err := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-secrets", TargetDir: "/repo", ApplicationID: &app.ID})
		if err != nil {
			t.Fatal(err)
		}
		if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
			t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
		}
	}
	run()
	run() // identical re-scan — every finding is already on file

	if obs, _ := db.ListObservationsByProject(ctx, proj.ID); len(obs) != 2 {
		t.Fatalf("re-scan should not duplicate observations, got %d want 2", len(obs))
	}
	if f, _ := db.ListFindings(ctx); len(f) != 1 {
		t.Fatalf("re-scan should not duplicate findings, got %d want 1", len(f))
	}
	if inv, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(inv) != 1 {
		t.Fatalf("re-scan should not duplicate investigations, got %d want 1", len(inv))
	}
	// Dedup requires a fingerprint to have been recorded on each observation.
	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	for _, o := range obs {
		if o.Fingerprint == "" {
			t.Fatalf("observation %s missing fingerprint", o.ID)
		}
	}
}

// reachCap is a fake reachability-SCA capability: its output is a govulncheck stream (one reachable + one
// imported-only vuln), and it declares the reachable+exposed disposition the real govulncheck ships (ADR-0030).
type reachCap struct{ dispositions []disposition.Disposition }

func (c reachCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "fake-reach", Version: "1.0.0", OutputName: "gv.json",
		OutputMediaType: interpret.GovulncheckMediaType, OKExitCodes: []int{0, 3},
		Dispositions: c.dispositions,
	}
}
func (reachCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

// GO-1/CVE-AAA is called (symbol-level trace → reachable); GO-2/CVE-BBB is only imported (→ not reachable).
const reachStream = `{"osv":{"id":"GO-1","aliases":["CVE-AAA"],"summary":"reachable vuln","affected":[{"package":{"name":"pkg/a"}}]}}
{"osv":{"id":"GO-2","aliases":["CVE-BBB"],"summary":"imported vuln","affected":[{"package":{"name":"pkg/b"}}]}}
{"finding":{"osv":"GO-1","trace":[{"module":"pkg/a","package":"pkg/a","function":"Vuln","position":{"filename":"a.go","line":10}}]}}
{"finding":{"osv":"GO-2","trace":[{"module":"pkg/b","package":"pkg/b"}]}}`

func reachEngine(t *testing.T) (*Engine, *store.DB) {
	t.Helper()
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(reachCap{dispositions: []disposition.Disposition{
		{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
	}})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(reachStream), code: 0})
	t.Cleanup(func() { eng.Close() })
	return eng, db
}

// A reachable vuln on an exposed service escalates to an investigation; the imported-but-uncalled one does not.
func TestReachabilityRoutesOnExposedService(t *testing.T) {
	eng, db := reachEngine(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	// A cloud_deployment asset marks the service network-exposed.
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetCloudDeployment, Location: "prod"}); err != nil {
		t.Fatal(err)
	}

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-reach", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(invs) != 1 {
		t.Fatalf("want 1 investigation (reachable+exposed only), got %d", len(invs))
	}
	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	if len(obs) != 2 {
		t.Fatalf("want 2 observations, got %d", len(obs))
	}
	for _, o := range obs {
		if o.Attributes["exposed"] != "true" {
			t.Fatalf("observation %s exposed = %q, want true", o.RuleID, o.Attributes["exposed"])
		}
	}
	// The analyzer's verdicts are recorded for other tools to reuse (ADR-0031 correlation).
	if reachable, known := db.ReachabilityForCVE(ctx, proj.ID, "CVE-AAA"); !known || !reachable {
		t.Fatalf("CVE-AAA verdict not recorded: known=%v reachable=%v", known, reachable)
	}
	if reachable, known := db.ReachabilityForCVE(ctx, proj.ID, "CVE-BBB"); !known || reachable {
		t.Fatalf("CVE-BBB verdict not recorded as unreachable: known=%v reachable=%v", known, reachable)
	}
}

// Reachable but on a service with no exposure evidence → manual review, no investigation.
func TestReachabilityNotExposedStaysReview(t *testing.T) {
	eng, db := reachEngine(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app") // no exposure evidence

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-reach", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	if inv, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(inv) != 0 {
		t.Fatalf("not-exposed should not escalate, got %d investigations", len(inv))
	}
	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	if len(obs) != 2 {
		t.Fatalf("want 2 observations, got %d", len(obs))
	}
	for _, o := range obs {
		if o.Attributes["exposed"] != "false" {
			t.Fatalf("observation %s exposed = %q, want false", o.RuleID, o.Attributes["exposed"])
		}
	}
}

// grypeLikeCap is a fake general SCA tool: SARIF output with CVE rule ids, routed by the same reachability
// rules grype ships (ADR-0031) — reachable:false→review, reachable+exposed→investigate, high→investigate.
type grypeLikeCap struct{}

func (grypeLikeCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "fake-grype", Version: "1.0.0", OutputName: "g.sarif",
		OutputMediaType: interpret.SARIFMediaType, OKExitCodes: []int{0},
		Dispositions: []disposition.Disposition{
			{When: map[string]string{"reachable": "false"}, Action: disposition.ActionReview},
			{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
			{MinSeverity: "high", Action: disposition.ActionInvestigate},
		},
	}
}
func (grypeLikeCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

// Three high-severity CVEs: AAA (govulncheck: reachable), BBB (govulncheck: not reachable), CCC (no verdict).
const grypeSARIF = `{"runs":[{"tool":{"driver":{"name":"grype"}},"results":[
  {"ruleId":"CVE-AAA","level":"error","message":{"text":"vuln a"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"go.mod"},"region":{"startLine":1}}}]},
  {"ruleId":"CVE-BBB","level":"error","message":{"text":"vuln b"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"go.mod"},"region":{"startLine":2}}}]},
  {"ruleId":"CVE-CCC","level":"error","message":{"text":"vuln c"},"locations":[{"physicalLocation":{"artifactLocation":{"uri":"package.json"},"region":{"startLine":3}}}]}
]}]}`

// A grype CVE inherits govulncheck's reachability verdict: an uncalled one is downgraded to review even
// though grype rates it high; a reachable one on an exposed service escalates; one with no verdict falls
// back to severity (ADR-0031).
func TestGrypeInheritsReachabilityVerdict(t *testing.T) {
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(grypeLikeCap{})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(grypeSARIF), code: 0})
	defer eng.Close()
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetCloudDeployment, Location: "prod"}); err != nil {
		t.Fatal(err)
	}
	// Seed govulncheck's verdicts (as a prior govulncheck run would have via the engine).
	_ = db.SetReachability(ctx, proj.ID, "CVE-AAA", "pkg/a", true, "govulncheck")
	_ = db.SetReachability(ctx, proj.ID, "CVE-BBB", "pkg/b", false, "govulncheck")

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-grype", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	byCVE := map[string]model.Observation{}
	for _, o := range obs {
		byCVE[o.RuleID] = o
	}
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	investigated := map[string]bool{}
	for _, iv := range invs {
		investigated[iv.ObservationID] = true
	}

	// AAA: enriched reachable=true + exposed → investigation.
	if byCVE["CVE-AAA"].Attributes["reachable"] != "true" {
		t.Fatalf("CVE-AAA reachable = %q, want true (inherited)", byCVE["CVE-AAA"].Attributes["reachable"])
	}
	if !investigated[byCVE["CVE-AAA"].ID] {
		t.Fatal("CVE-AAA (reachable+exposed) should open an investigation")
	}
	// BBB: enriched reachable=false → downgraded to review despite high severity, no investigation.
	if byCVE["CVE-BBB"].Attributes["reachable"] != "false" {
		t.Fatalf("CVE-BBB reachable = %q, want false (inherited)", byCVE["CVE-BBB"].Attributes["reachable"])
	}
	if investigated[byCVE["CVE-BBB"].ID] {
		t.Fatal("CVE-BBB proved unreachable should NOT escalate (the core of ADR-0031)")
	}
	// CCC: no verdict → severity fallback → investigation.
	if _, has := byCVE["CVE-CCC"].Attributes["reachable"]; has {
		t.Fatal("CVE-CCC has no verdict; reachable should be unset")
	}
	if !investigated[byCVE["CVE-CCC"].ID] {
		t.Fatal("CVE-CCC (no verdict, high) should investigate via severity fallback")
	}
}

// semgrepLikeCap is a fake SAST tool: SARIF with a taint finding (codeFlows → reachable) and plain pattern
// findings, routed by semgrep's dataflow-reachability rules (ADR-0032).
type semgrepLikeCap struct{}

func (semgrepLikeCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "fake-semgrep", Version: "1.0.0", OutputName: "s.sarif",
		OutputMediaType: interpret.SARIFMediaType, OKExitCodes: []int{0, 1},
		Dispositions: []disposition.Disposition{
			{When: map[string]string{"reachable": "true", "exposed": "true"}, Action: disposition.ActionInvestigate},
			{MinSeverity: "high", Action: disposition.ActionInvestigate},
		},
	}
}
func (semgrepLikeCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

// taint.sql is a MEDIUM dataflow finding (has codeFlows); pattern.high is a plain HIGH; pattern.low a plain LOW.
const semgrepSARIF = `{"runs":[{"tool":{"driver":{"name":"semgrep"}},"results":[
  {"ruleId":"taint.sql","level":"warning","message":{"text":"SQLi"},
   "locations":[{"physicalLocation":{"artifactLocation":{"uri":"a.py"},"region":{"startLine":42}}}],
   "codeFlows":[{"threadFlows":[{"locations":[
     {"location":{"physicalLocation":{"artifactLocation":{"uri":"a.py"},"region":{"startLine":12}}}}]}]}]},
  {"ruleId":"pattern.high","level":"error","message":{"text":"hardcoded key"},
   "locations":[{"physicalLocation":{"artifactLocation":{"uri":"b.py"},"region":{"startLine":1}}}]},
  {"ruleId":"pattern.low","level":"note","message":{"text":"style"},
   "locations":[{"physicalLocation":{"artifactLocation":{"uri":"c.py"},"region":{"startLine":1}}}]}
]}]}`

// On an exposed service: a dataflow-reachable finding escalates even at medium severity; a plain high still
// escalates on severity; a plain low stays in review (ADR-0032).
func TestSastDataflowRoutingOnExposedService(t *testing.T) {
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(semgrepLikeCap{})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(semgrepSARIF), code: 0})
	defer eng.Close()
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetCloudDeployment, Location: "prod"}); err != nil {
		t.Fatal(err)
	}

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-semgrep", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	byRule := map[string]model.Observation{}
	for _, o := range obs {
		byRule[o.RuleID] = o
	}
	invs, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	investigated := map[string]bool{}
	for _, iv := range invs {
		investigated[iv.ObservationID] = true
	}

	// The medium taint finding is reachable and escalates on the exposed service.
	if byRule["taint.sql"].Severity != "medium" || byRule["taint.sql"].Attributes["reachable"] != "true" {
		t.Fatalf("taint.sql sev=%q reachable=%q, want medium/true", byRule["taint.sql"].Severity, byRule["taint.sql"].Attributes["reachable"])
	}
	if !investigated[byRule["taint.sql"].ID] {
		t.Fatal("dataflow-reachable medium finding on exposed service should investigate")
	}
	// The plain high finding still escalates on severity; the plain low stays in review.
	if !investigated[byRule["pattern.high"].ID] {
		t.Fatal("plain high finding should investigate via severity fallback")
	}
	if investigated[byRule["pattern.low"].ID] {
		t.Fatal("plain low finding should stay in review")
	}
}

// routeMapCap is a fake route-map: its output is route JSON (parsed to routes, not observations).
type routeMapCap struct{}

func (routeMapCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "fake-routemap", Version: "1.0.0", OutputName: "routes.json",
		OutputMediaType: interpret.RouteMediaType, OKExitCodes: []int{0, 1},
	}
}
func (routeMapCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

const routeMapJSON = `{"results":[
  {"check_id":"osb-route-flask","path":"app/views.py","start":{"line":8},
   "extra":{"metavars":{"$ROUTE":{"abstract_content":"\"/users/<id>\""}},"metadata":{"framework":"flask"}}}
]}`

// The route-map capability's output populates the routes inventory (not observations), and captured traffic
// to a matching path confirms the route as exposed (ADR-0033).
func TestRouteMapPopulatesInventory(t *testing.T) {
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(routeMapCap{})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(routeMapJSON), code: 0})
	defer eng.Close()
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	// A captured request to the route's path — should flip it to observed.
	if _, err := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: proj.ID, Origin: "proxy", Method: "GET", URL: "http://app/users/42"}); err != nil {
		t.Fatal(err)
	}

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-routemap", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	// The route was recorded, no observations were created, and traffic confirmed it exposed.
	if obs, _ := db.ListObservationsByProject(ctx, proj.ID); len(obs) != 0 {
		t.Fatalf("route-map output should create no observations, got %d", len(obs))
	}
	routes, _ := db.ListRoutesByProject(ctx, proj.ID)
	if len(routes) != 1 {
		t.Fatalf("want 1 route, got %d", len(routes))
	}
	if routes[0].Path != "/users/<id>" || routes[0].Framework != "flask" || routes[0].HandlerFile != "app/views.py" {
		t.Fatalf("route = %+v", routes[0])
	}
	if !routes[0].Observed {
		t.Fatal("route matched by captured traffic should be observed")
	}
}

// A SAST finding in a file that declares an exposed route is tied to that entry point via an exposed_route
// attribute (ADR-0033). A finding in a file with no route is not.
func TestExposedRouteAssociation(t *testing.T) {
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(semgrepLikeCap{})
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(semgrepSARIF), code: 0})
	defer eng.Close()
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	// Exposed service, with a traffic-confirmed route whose handler is a.py (the taint finding's file).
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetCloudDeployment, Location: "prod"}); err != nil {
		t.Fatal(err)
	}
	_ = db.UpsertRoute(ctx, model.Route{ProjectID: proj.ID, Method: "POST", Path: "/query", HandlerFile: "a.py", Source: "route-map", Observed: true})

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-semgrep", TargetDir: "/repo", ApplicationID: &app.ID})
	if done := pollTask(t, eng, tk.ID); done.Status != model.TaskSucceeded {
		t.Fatalf("task = %s (err=%q)", done.Status, done.Error)
	}

	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	byRule := map[string]model.Observation{}
	for _, o := range obs {
		byRule[o.RuleID] = o
	}
	// taint.sql is at a.py:42 → tied to the exposed route in a.py, and marked traffic-confirmed.
	if got := byRule["taint.sql"].Attributes["exposed_route"]; got != "POST /query" {
		t.Fatalf("taint.sql exposed_route = %q, want \"POST /query\"", got)
	}
	if byRule["taint.sql"].Attributes["route_observed"] != "true" {
		t.Fatalf("taint.sql route_observed = %q, want true", byRule["taint.sql"].Attributes["route_observed"])
	}
	// pattern.high is at b.py:1 → no route in that file, no association.
	if _, has := byRule["pattern.high"].Attributes["exposed_route"]; has {
		t.Fatal("pattern.high is in b.py (no route) — should have no exposed_route")
	}
}

// A capability with no dispositions leaves plain unreviewed observations (no regression).
func TestNoDispositionsLeavesReview(t *testing.T) {
	db, blobs := openStore(t)
	reg := capability.NewRegistry()
	reg.Register(dispoCap{}) // no dispositions
	eng := NewEngine(db, blobs, reg, fakeRunner{out: []byte(twoSecrets), code: 0})
	defer eng.Close()
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")

	tk, _ := eng.Enqueue(ctx, RunRequest{CapabilityID: "fake-secrets", TargetDir: "/r", ApplicationID: &app.ID})
	pollTask(t, eng, tk.ID)

	if f, _ := db.ListFindings(ctx); len(f) != 0 {
		t.Fatalf("no dispositions should create no findings, got %d", len(f))
	}
	if inv, _ := db.ListInvestigationsByProject(ctx, proj.ID); len(inv) != 0 {
		t.Fatalf("no dispositions should create no investigations, got %d", len(inv))
	}
	obs, _ := db.ListObservationsByProject(ctx, proj.ID)
	if len(obs) != 2 {
		t.Fatalf("want 2 plain observations, got %d", len(obs))
	}
	for _, o := range obs {
		if o.ReviewState != model.ReviewUnreviewed {
			t.Fatalf("observation should be unreviewed, got %s", o.ReviewState)
		}
	}
}
