package srcfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestConfinedPathRejectsEscape(t *testing.T) {
	root := t.TempDir()
	escapes := []string{
		"../etc/passwd",
		"../../secret",
		"a/../../b",
	}
	for _, rel := range escapes {
		if _, err := ConfinedPath(root, rel); err == nil {
			t.Errorf("ConfinedPath(%q) allowed an escape; want error", rel)
		}
	}
}

// An absolute-looking rel is not an escape: filepath.Join re-roots it under the asset dir, so it stays
// confined (and simply won't exist). Verify it lands inside root rather than reaching the real path.
func TestConfinedPathNeutralizesAbsolute(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"/etc/passwd", filepath.Join(root, "..", "sibling")} {
		got, err := ConfinedPath(root, rel)
		if err != nil {
			continue // erroring is also acceptable
		}
		if got != root && got[:len(root)+1] != root+string(filepath.Separator) {
			t.Errorf("ConfinedPath(%q) = %q, escaped root %q", rel, got, root)
		}
	}
}

func TestConfinedPathAllowsInside(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"", ".", "app", "app/views.py", "./app/../app"} {
		if _, err := ConfinedPath(root, rel); err != nil {
			t.Errorf("ConfinedPath(%q) rejected an in-root path: %v", rel, err)
		}
	}
}

func TestConfinedPathRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := ConfinedPath(root, "escape/secret"); err == nil {
		t.Error("ConfinedPath followed a symlink out of the root; want error")
	}
}

func TestReadFileTruncatesAndCounts(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("a\nb\nc"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := ReadFile(root, "f.txt", 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Lines != 3 || f.Content != "a\nb\nc" {
		t.Errorf("got lines=%d content=%q", f.Lines, f.Content)
	}
	small, err := ReadFile(root, "f.txt", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !small.Truncated || small.Content != "a\n" {
		t.Errorf("expected truncation, got truncated=%v content=%q", small.Truncated, small.Content)
	}
}

func TestReadFileRejectsDir(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "d", 0); err == nil {
		t.Error("ReadFile on a directory should error")
	}
}

func TestListDirOrdersAndSkipsNoise(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"src", ".git", "node_modules"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, err := ListDir(root, "")
	if err != nil {
		t.Fatal(err)
	}
	// .git and node_modules skipped → only "src" (dir) then "README.md" (file).
	if len(entries) != 2 || !entries[0].Dir || entries[0].Name != "src" || entries[1].Name != "README.md" {
		t.Fatalf("unexpected listing: %+v", entries)
	}
	if entries[0].Path != "src" {
		t.Errorf("child path should be root-relative, got %q", entries[0].Path)
	}
	// Nested listing carries the relative prefix.
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested, err := ListDir(root, "src")
	if err != nil {
		t.Fatal(err)
	}
	if len(nested) != 1 || nested[0].Path != "src/main.go" {
		t.Fatalf("nested path wrong: %+v", nested)
	}
}
