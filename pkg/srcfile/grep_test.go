package srcfile

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestGrep(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "app/auth.py", "def login():\n    secret = get_SECRET_key()\n    return secret\n")
	mk(t, root, "app/util.py", "x = 1\n")
	// Noise + binary must be ignored.
	mk(t, root, "node_modules/lib/index.js", "const secret = 42\n")
	mk(t, root, ".git/config", "secret\n")
	mk(t, root, "app/blob.bin", "sec\x00ret")

	hits := Grep(root, "secret", 100, 1<<20)
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2 (both lines in auth.py); hits=%+v", len(hits), hits)
	}
	// Case-insensitive, correct file/line/text.
	if hits[0].Path != filepath.Join("app", "auth.py") || hits[0].Line != 2 {
		t.Fatalf("first hit = %+v, want app/auth.py:2", hits[0])
	}
	if hits[0].Text != "secret = get_SECRET_key()" {
		t.Fatalf("hit text = %q", hits[0].Text)
	}
	for _, h := range hits {
		if h.Path == filepath.Join("node_modules", "lib", "index.js") || h.Path == filepath.Join(".git", "config") {
			t.Fatalf("noise dir should be skipped: %+v", h)
		}
	}
}

func TestGrepBounded(t *testing.T) {
	root := t.TempDir()
	mk(t, root, "a.txt", "match\nmatch\nmatch\nmatch\n")
	if hits := Grep(root, "match", 2, 1<<20); len(hits) != 2 {
		t.Fatalf("maxMatches=2 → want 2, got %d", len(hits))
	}
	if hits := Grep(root, "", 10, 1<<20); hits != nil {
		t.Fatal("empty needle → nil")
	}
}
