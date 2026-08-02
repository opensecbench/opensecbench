package store

import (
	"context"
	"os"
	"path/filepath"
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
