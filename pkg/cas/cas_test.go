package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
	"testing"
)

func TestPutIsContentAddressedAndIdempotent(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	const content = "cross-tenant write succeeded"
	want := sha256.Sum256([]byte(content))
	wantHex := hex.EncodeToString(want[:])

	d1, err := s.Put(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if d1 != wantHex {
		t.Fatalf("digest = %s, want %s", d1, wantHex)
	}

	d2, err := s.Put(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	if d2 != d1 {
		t.Fatalf("re-store gave a different digest: %s vs %s", d2, d1)
	}

	rc, err := s.Open(d1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Fatalf("read back %q, want %q", got, content)
	}
}

func TestOpenRejectsBadDigest(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open("x"); err == nil {
		t.Fatal("expected error for invalid digest")
	}
}
