package task

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

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
