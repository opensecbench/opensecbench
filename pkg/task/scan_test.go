package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/runner"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// pyOnlyCap is a language-specific fake: it declares the python ecosystem, so the scan gate must keep it
// off a non-python repo.
type pyOnlyCap struct{}

func (pyOnlyCap) Manifest() capability.Manifest {
	return capability.Manifest{
		ID: "py-checker", Version: "1.0.0", OutputName: "o",
		AppliesTo: []string{"source_repo"}, Ecosystems: []string{"python"}, OKExitCodes: []int{0},
	}
}
func (pyOnlyCap) Plan(capability.Input) (runner.RunSpec, error) {
	return runner.RunSpec{Image: "x", Cmd: []string{"x"}, Timeout: time.Minute}, nil
}

func seedRepoAsset(t *testing.T, db *store.DB, projectID, marker, content string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, marker), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	app, _ := db.CreateApplication(ctx, projectID, marker)
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: dir, Sensitivity: "private"}); err != nil {
		t.Fatal(err)
	}
}

// A language-specific capability runs only on a repo whose stack it targets — a Python checker is kept off
// a Rust app and fired on a Python one.
func TestScanProjectGatesLanguageSpecificTool(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	reg := capability.NewRegistry()
	reg.Register(pyOnlyCap{})
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), reg, fakeRunner{code: 0})
	defer eng.Close()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	seedRepoAsset(t, db, proj.ID, "Cargo.toml", "[package]\n") // a Rust app

	res, err := eng.ScanProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tk := range res.Enqueued {
		if tk.CapabilityID == "py-checker" {
			t.Fatal("python checker must not run on a rust repo")
		}
	}
	var skipped bool
	for _, s := range res.Skipped {
		if s.CapabilityID == "py-checker" {
			skipped = true
		}
	}
	if !skipped {
		t.Fatalf("py-checker should be a recorded skip; skips=%v", res.Skipped)
	}

	// Add a Python app — now the checker fires (on that asset).
	seedRepoAsset(t, db, proj.ID, "requirements.txt", "flask\n")
	res2, _ := eng.ScanProject(ctx, proj.ID)
	var ran bool
	for _, tk := range res2.Enqueued {
		if tk.CapabilityID == "py-checker" {
			ran = true
		}
	}
	if !ran {
		t.Fatal("python checker should run on a python repo")
	}
}

func enqueuedHas(res ScanResult, cap string) bool {
	for _, tk := range res.Enqueued {
		if tk.CapabilityID == cap {
			return true
		}
	}
	return false
}

// A manual ecosystem tag makes a tool run where detection missed the stack — the operator override for
// a polyglot/unusual repo. It's unioned with detection and normalized (Python → python).
func TestScanProjectManualEcosystemTagOverride(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	reg := capability.NewRegistry()
	reg.Register(pyOnlyCap{})
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), reg, fakeRunner{code: 0})
	defer eng.Close()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]"), 0o600); err != nil {
		t.Fatal(err)
	}
	asset, _ := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: dir, Sensitivity: "private"})

	res, _ := eng.ScanProject(ctx, proj.ID)
	if enqueuedHas(res, "py-checker") {
		t.Fatal("py-checker should be skipped before tagging (rust repo, no python markers)")
	}

	// Operator tags the repo python — detection under-read it.
	if _, err := db.SetAssetEcosystems(ctx, asset.ID, []string{"Python"}); err != nil {
		t.Fatal(err)
	}
	res2, _ := eng.ScanProject(ctx, proj.ID)
	if !enqueuedHas(res2, "py-checker") {
		t.Fatal("py-checker should run after the manual python tag")
	}
}

// ScanProject fans every applicable capability across a source_repo asset, skips a language-specific one
// whose ecosystem isn't detected, and never fires a capability that opts out (no AppliesTo).
func TestScanProjectFansOutApplicableCapabilities(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()

	// A Python-ish repo: no go.mod, so govulncheck (ecosystem "go") must be skipped.
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "requirements.txt"), []byte("flask\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo, Sensitivity: "private"}); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("x\n"), code: 0})
	defer eng.Close()

	res, err := eng.ScanProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}

	enq := map[string]bool{}
	for _, tk := range res.Enqueued {
		enq[tk.CapabilityID] = true
	}
	// Language-agnostic source scanners must all fire.
	for _, want := range []string{"source-inventory", "syft", "grype", "osv-scanner", "opengrep", "route-map"} {
		if !enq[want] {
			t.Errorf("expected %q to be enqueued; got %v", want, enq)
		}
	}
	// semgrep opts out of auto-scan (opengrep is the default SAST).
	if enq["semgrep"] {
		t.Error("semgrep should not auto-run (no AppliesTo)")
	}
	// govulncheck is Go-only and this repo has no go.mod → skipped, not enqueued.
	if enq["govulncheck"] {
		t.Error("govulncheck should be skipped for a non-Go repo")
	}
	var govulnSkipped bool
	for _, s := range res.Skipped {
		if s.CapabilityID == "govulncheck" {
			govulnSkipped = true
		}
	}
	if !govulnSkipped {
		t.Errorf("govulncheck should appear in Skipped with an ecosystem reason; skips=%v", res.Skipped)
	}
}

// A Go repo (go.mod present) does get govulncheck.
func TestScanProjectRunsGoScannerOnGoRepo(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module x\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	if _, err := db.CreateAsset(ctx, store.NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: repo, Sensitivity: "private"}); err != nil {
		t.Fatal(err)
	}
	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{out: []byte("x\n"), code: 0})
	defer eng.Close()

	res, err := eng.ScanProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	var govuln bool
	for _, tk := range res.Enqueued {
		if tk.CapabilityID == "govulncheck" {
			govuln = true
		}
	}
	if !govuln {
		t.Error("govulncheck should be enqueued for a Go repo (go.mod present)")
	}
}
