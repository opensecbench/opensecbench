package srcfile

import (
	"errors"
	"io/fs"
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

// Resolve must re-anchor a finding location that carries a scanner's container-mount prefix. TruffleHog
// (`filesystem /src`) and govulncheck emit absolute "/src/app/x.go" paths; the on-disk asset root is the
// repo, so the file lives at "app/x.go". A `file://` URI must resolve the same way.
func TestResolveReAnchorsMountPrefix(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "x.go"), []byte("package app\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "app", "x.go")
	for _, rel := range []string{"app/x.go", "/src/app/x.go", "src/app/x.go", "file:///src/app/x.go"} {
		got, err := Resolve(root, rel)
		if err != nil {
			t.Errorf("Resolve(%q) errored: %v", rel, err)
			continue
		}
		if got != want {
			t.Errorf("Resolve(%q) = %q, want %q", rel, got, want)
		}
		// ReadFile reports the clean, re-anchored path regardless of the prefix it was handed.
		f, err := ReadFile(root, rel, 0)
		if err != nil {
			t.Errorf("ReadFile(%q) errored: %v", rel, err)
		} else if f.Path != "app/x.go" {
			t.Errorf("ReadFile(%q).Path = %q, want app/x.go", rel, f.Path)
		}
	}
}

// A genuinely missing file resolves to a wrapped fs.ErrNotExist that errors.Is detects (so the HTTP layer
// maps it to 404), while a real traversal escape stays a distinct error (mapped to 4xx, not 404).
func TestResolveMissingVsEscape(t *testing.T) {
	root := t.TempDir()
	_, err := Resolve(root, "app/missing.go")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing file: errors.Is(fs.ErrNotExist) = false, err = %v", err)
	}
	if _, err := Resolve(root, "../../etc/passwd"); err == nil || errors.Is(err, fs.ErrNotExist) {
		t.Errorf("traversal should be a non-not-exist error, got %v", err)
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
