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
