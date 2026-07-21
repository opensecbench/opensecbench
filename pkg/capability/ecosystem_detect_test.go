package capability

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, rel string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Detection finds nested manifests (a monorepo), not just the root, and ignores noise directories.
func TestDetectEcosystemsMonorepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod")                            // a go module at the root
	write(t, root, "services/web/package.json")         // a node service nested
	write(t, root, "services/scraper/requirements.txt") // a python service nested
	// Noise that must be ignored — a dependency's own manifests.
	write(t, root, "node_modules/left-pad/package.json")
	write(t, root, "vendor/rust-crate/Cargo.toml")
	// Too deep — beyond the bounded walk.
	write(t, root, "a/b/c/d/e/f/Gemfile")

	got := DetectEcosystems(root)
	for _, want := range []string{"go", "node", "python"} {
		if !got[want] {
			t.Errorf("expected %q detected in the monorepo; got %v", want, got)
		}
	}
	if got["rust"] {
		t.Errorf("rust from vendor/ should be ignored; got %v", got)
	}
	if got["ruby"] {
		t.Errorf("Gemfile beyond the depth bound should not be detected; got %v", got)
	}
}

func TestDetectEcosystemsEmpty(t *testing.T) {
	if len(DetectEcosystems("")) != 0 {
		t.Fatal("empty dir → empty set")
	}
	if len(DetectEcosystems(t.TempDir())) != 0 {
		t.Fatal("a repo with no manifests → empty set")
	}
}
