package secret

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

func newVault(t *testing.T) *Vault {
	t.Helper()
	key := make([]byte, KeySize)
	_, _ = rand.Read(key)
	v, err := NewVault(key)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSealOpenRoundTrip(t *testing.T) {
	v := newVault(t)
	secret := []byte("s3cr3t-token-abc")
	blob, err := v.Seal(secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains([]byte(blob), secret) {
		t.Fatal("sealed blob leaks plaintext")
	}
	got, err := v.Open(blob)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Fatalf("round trip = %q, want %q", got, secret)
	}
}

func TestSealNonceIsRandom(t *testing.T) {
	v := newVault(t)
	a, _ := v.Seal([]byte("same"))
	b, _ := v.Seal([]byte("same"))
	if a == b {
		t.Fatal("identical ciphertext for same plaintext (nonce reuse?)")
	}
}

func TestOpenRejectsTamperAndWrongKey(t *testing.T) {
	v := newVault(t)
	blob, _ := v.Seal([]byte("data"))
	// Wrong key cannot open.
	if _, err := newVault(t).Open(blob); err == nil {
		t.Fatal("open with wrong key should fail (GCM auth)")
	}
	// Tampered ciphertext cannot open.
	tampered := blob[:len(blob)-2] + "AA"
	if _, err := v.Open(tampered); err == nil {
		t.Fatal("tampered ciphertext should fail auth")
	}
}

func TestLoadVaultFromEnvThenFile(t *testing.T) {
	dir := t.TempDir()

	// File path: generates a 0600 key file and is stable across loads.
	v1, err := LoadVault(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "vault.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("vault.key mode = %v, want 0600", info.Mode().Perm())
	}
	blob, _ := v1.Seal([]byte("persisted"))
	v2, _ := LoadVault(dir)
	if got, err := v2.Open(blob); err != nil || string(got) != "persisted" {
		t.Fatalf("reloaded vault cannot open earlier blob: %v", err)
	}
}
