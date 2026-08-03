package hub

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func writePkg(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"acme.tool","name":"Tool","version":"1.2.0","publisher":"acme",
	 "capabilities":[{"id":"tool","version":"1.2.0","title":"Tool","image":"alpine:3","cmd":["echo","hi"]}]}`
	if err := os.WriteFile(filepath.Join(dir, "extension.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extension.sig"), []byte("c2ln"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublishServeInstallRoundTrip(t *testing.T) {
	hubDir := t.TempDir()
	pkgDir := filepath.Join(t.TempDir(), "tool")
	writePkg(t, pkgDir)

	entry, err := Publish(hubDir, pkgDir, "cHVia2V5", "a tool", []string{"secrets"})
	if err != nil {
		t.Fatal(err)
	}
	if entry.ID != "acme.tool" || entry.Version != "1.2.0" || entry.Digest == "" {
		t.Fatalf("bad entry: %+v", entry)
	}

	// Serve the hub directory over HTTP.
	srv := httptest.NewServer(http.FileServer(http.Dir(hubDir)))
	defer srv.Close()
	client := NewClient(0)
	client.allowLoopback = true // httptest serves on loopback
	ctx := context.Background()

	idx, err := client.FetchIndex(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := idx.Find("acme.tool")
	if !ok || got.PublisherKey != "cHVia2V5" || len(got.Tags) != 1 {
		t.Fatalf("index entry wrong: %+v", got)
	}

	archive, err := client.DownloadArchive(ctx, srv.URL, got)
	if err != nil {
		t.Fatal(err)
	}

	// Extract and confirm the package files came through.
	dst := filepath.Join(t.TempDir(), "installed")
	if err := Extract(archive, dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "extension.json")); err != nil {
		t.Fatalf("manifest not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "extension.sig")); err != nil {
		t.Fatalf("signature not extracted: %v", err)
	}
}

func TestDownloadRejectsTamperedArchive(t *testing.T) {
	hubDir := t.TempDir()
	pkgDir := filepath.Join(t.TempDir(), "tool")
	writePkg(t, pkgDir)
	entry, _ := Publish(hubDir, pkgDir, "k", "", nil)

	// Corrupt the archive on disk after publishing (digest in the index no longer matches).
	archivePath := filepath.Join(hubDir, entry.Archive)
	if err := os.WriteFile(archivePath, []byte("not the real archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(hubDir)))
	defer srv.Close()

	tc := NewClient(0)
	tc.allowLoopback = true // httptest serves on loopback
	if _, err := tc.DownloadArchive(context.Background(), srv.URL, entry); err == nil {
		t.Fatal("tampered archive should fail the digest check")
	}
}
