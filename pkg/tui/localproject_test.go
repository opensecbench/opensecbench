package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMarkerDiscoverAndWrite covers binding a directory to a project and discovering it from a subdir.
func TestMarkerDiscoverAndWrite(t *testing.T) {
	root := t.TempDir()

	if id := DiscoverProject(root); id != "" {
		t.Fatalf("no marker yet, want empty, got %q", id)
	}

	if err := writeMarker(root, "proj-1", "Acme"); err != nil {
		t.Fatal(err)
	}
	if id := DiscoverProject(root); id != "proj-1" {
		t.Fatalf("discover at root = %q, want proj-1", id)
	}

	// Discovery walks up the tree (like git finding .git).
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if id := DiscoverProject(sub); id != "proj-1" {
		t.Fatalf("discover from subdir = %q, want proj-1 (walk-up)", id)
	}

	// The marker lives alongside the project's data under cwd/.opensecbench.
	if _, err := os.Stat(filepath.Join(root, projectDirName, projectMarker)); err != nil {
		t.Fatalf("marker not written where expected: %v", err)
	}
}
