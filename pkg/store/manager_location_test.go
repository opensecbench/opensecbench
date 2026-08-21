package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

// TestCustomProjectLocation verifies the opt-in containment behavior: a project created with a Location
// keeps its whole self-contained dir in a subfolder there (nothing leaks to the default data dir), the
// override survives a Manager reopen (loaded from the index), and deleting the project removes only that
// subfolder — never the user's sibling files.
func TestCustomProjectLocation(t *testing.T) {
	root := t.TempDir()
	m, err := OpenManager(root, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	loc := t.TempDir() // the user's engagement directory
	sibling := filepath.Join(loc, "source.txt")
	if err := os.WriteFile(sibling, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	p, err := m.CreateProject(ctx, NewProject{Name: "Acme", Location: loc})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(loc, ProjectSubdir)
	if got := m.ProjectDir(p.ID); got != want {
		t.Fatalf("ProjectDir = %q, want %q", got, want)
	}
	if m.ProjectCASDir(p.ID) != filepath.Join(want, "cas") {
		t.Fatalf("CAS dir should sit inside the custom location, got %q", m.ProjectCASDir(p.ID))
	}
	if _, err := os.Stat(filepath.Join(want, "project.db")); err != nil {
		t.Fatalf("project.db not in the custom location: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "projects", p.ID)); !os.IsNotExist(err) {
		t.Fatal("nothing should be created in the default projects/ location")
	}

	// Reopen the Manager: the custom location must load from the index, not reset to default.
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	m2, err := OpenManager(root, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	if got := m2.ProjectDir(p.ID); got != want {
		t.Fatalf("after reopen ProjectDir = %q, want %q", got, want)
	}

	// Delete removes only our subfolder; the user's sibling file survives.
	if err := m2.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(want); !os.IsNotExist(err) {
		t.Fatal("the .opensecbench subfolder should be gone after delete")
	}
	if _, err := os.Stat(sibling); err != nil {
		t.Fatalf("the user's sibling file must survive a project delete: %v", err)
	}
	_ = m2.Close()
}

// TestAdoptProject verifies that a foreign project directory (one created by another instance) can be
// adopted into this instance's index, survives a Manager reopen, and guards against collisions.
func TestAdoptProject(t *testing.T) {
	ctx := context.Background()

	// Instance A: create a project with a custom location.
	rootA := t.TempDir()
	mA, err := OpenManager(rootA, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	loc := t.TempDir()
	p, err := mA.CreateProject(ctx, NewProject{Name: "Foreign", Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	_ = mA.Close()

	// Write a marker (the TUI does this; simulate it).
	markerDir := filepath.Join(loc, ProjectSubdir)
	mb, _ := json.MarshalIndent(struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}{p.ID, p.Name}, "", "  ")
	_ = os.WriteFile(filepath.Join(markerDir, "project.json"), mb, 0o644)

	// Instance B: a fresh instance that knows nothing about the project.
	rootB := t.TempDir()
	mB, err := OpenManager(rootB, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mB.Close() }()

	// The project is not known yet.
	if known, _ := mB.projectIndexed(ctx, p.ID); known {
		t.Fatal("project should not be known before adopt")
	}

	// Adopt it.
	adopted, err := mB.AdoptProject(ctx, loc)
	if err != nil {
		t.Fatalf("AdoptProject failed: %v", err)
	}
	if adopted.ID != p.ID {
		t.Fatalf("adopted id = %q, want %q", adopted.ID, p.ID)
	}
	if adopted.Name != "Foreign" {
		t.Fatalf("adopted name = %q, want %q", adopted.Name, "Foreign")
	}

	// It's now in the index and openable.
	got, err := mB.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProject after adopt: %v", err)
	}
	if got.Name != "Foreign" {
		t.Fatalf("got name %q after adopt", got.Name)
	}
	if dir := mB.ProjectDir(p.ID); dir != markerDir {
		t.Fatalf("ProjectDir = %q, want %q", dir, markerDir)
	}

	// Adopting the same location again is idempotent (returns the project, no error).
	again, err := mB.AdoptProject(ctx, loc)
	if err != nil {
		t.Fatalf("second adopt should be idempotent: %v", err)
	}
	if again.ID != p.ID {
		t.Fatalf("second adopt returned wrong id %q", again.ID)
	}

	// Guard: adopting from a directory with no .opensecbench fails.
	emptyDir := t.TempDir()
	if _, err := mB.AdoptProject(ctx, emptyDir); err == nil {
		t.Fatal("adopt should fail for a directory with no project.db")
	}

	// Guard: ID collision — create a different project with the same id in another location.
	locC := t.TempDir()
	mC, err := OpenManager(t.TempDir(), migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = mC.CreateProject(ctx, NewProject{Name: "Collider", Location: locC})
	_ = mC.Close()
	// Rewrite the marker in locC to use p.ID (simulating a real collision).
	collideDir := filepath.Join(locC, ProjectSubdir)
	mb2, _ := json.MarshalIndent(struct {
		ID string `json:"id"`
	}{p.ID}, "", "  ")
	_ = os.WriteFile(filepath.Join(collideDir, "project.json"), mb2, 0o644)
	_, err = mB.AdoptProject(ctx, locC)
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("adopt should fail for an id collision, got: %v", err)
	}

	// Survives a Manager reopen.
	_ = mB.Close()
	mB2, err := OpenManager(rootB, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	got2, err := mB2.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetProject after reopen: %v", err)
	}
	if got2.Name != "Foreign" {
		t.Fatalf("name after reopen = %q", got2.Name)
	}
	_ = mB2.Close()
}

// TestAdoptProjectSchemaTooNew verifies that adopting a project.db with a newer schema version than
// this build supports is refused.
func TestAdoptProjectSchemaTooNew(t *testing.T) {
	ctx := context.Background()

	// Create a project, then manually bump its schema version beyond what our migrations cover.
	rootA := t.TempDir()
	mA, err := OpenManager(rootA, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	loc := t.TempDir()
	p, err := mA.CreateProject(ctx, NewProject{Name: "Future", Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	_ = filepath.Join(loc, ProjectSubdir)
	pdb, err := mA.Project(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Insert a fake migration beyond the real set.
	_, _ = pdb.Exec(`INSERT INTO schema_migrations (version, name, applied_at) VALUES (99999, 'future', datetime('now'))`)
	_ = mA.Close()

	// Instance B tries to adopt — should refuse.
	rootB := t.TempDir()
	mB, err := OpenManager(rootB, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mB.Close() }()

	_, err = mB.AdoptProject(ctx, loc)
	if err == nil || !strings.Contains(err.Error(), "newer than this build") {
		t.Fatalf("expected schema-too-new error, got: %v", err)
	}
}

// TestAdoptProjectFallbackToDBRead verifies that adopt works even without a project.json marker,
// by reading the project id directly from the database.
func TestAdoptProjectFallbackToDBRead(t *testing.T) {
	ctx := context.Background()

	rootA := t.TempDir()
	mA, err := OpenManager(rootA, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	loc := t.TempDir()
	p, err := mA.CreateProject(ctx, NewProject{Name: "NoMarker", Location: loc})
	if err != nil {
		t.Fatal(err)
	}
	_ = mA.Close()
	// Remove the marker to force the DB fallback.
	_ = os.Remove(filepath.Join(loc, ProjectSubdir, "project.json"))

	rootB := t.TempDir()
	mB, err := OpenManager(rootB, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = mB.Close() }()

	adopted, err := mB.AdoptProject(ctx, loc)
	if err != nil {
		t.Fatalf("AdoptProject (no marker) failed: %v", err)
	}
	if adopted.ID != p.ID || adopted.Name != "NoMarker" {
		t.Fatalf("got %q / %q, want %q / %q", adopted.ID, adopted.Name, p.ID, "NoMarker")
	}
}
