package controlplane

import (
	"os"
	"runtime"
	"testing"
)

func TestLoadOrCreateAPIToken(t *testing.T) {
	dir := t.TempDir()

	tok, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tok) != 64 { // 32 random bytes, hex-encoded
		t.Fatalf("token length = %d, want 64 hex chars", len(tok))
	}

	// Persistent: a second load returns the same token (ADR-0061).
	tok2, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tok2 != tok {
		t.Fatalf("token not persistent: %q != %q", tok2, tok)
	}

	// Stored 0600 (skip the mode assertion on Windows, where Unix perms don't apply).
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(APITokenPath(dir))
		if err != nil {
			t.Fatal(err)
		}
		if perm := fi.Mode().Perm(); perm != 0o600 {
			t.Fatalf("token file mode = %o, want 600", perm)
		}
	}

	// An empty/corrupt file is regenerated rather than trusted as a blank token.
	if err := os.WriteFile(APITokenPath(dir), []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tok3, err := LoadOrCreateAPIToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if tok3 == "" || tok3 == tok {
		t.Fatalf("empty file not regenerated: got %q", tok3)
	}
}
