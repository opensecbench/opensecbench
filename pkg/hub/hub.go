// Package hub is the extension distribution layer (ADR-0014): a hub is a static index.json plus
// package archives, servable from a directory, git repo, or hosted service. Install reuses the
// extension loader's signature/trust verification — the hub is never a trust root.
package hub

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// IndexFile is the well-known index filename at a hub root.
const IndexFile = "index.json"

// PackageEntry describes one package available from a hub.
type PackageEntry struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Publisher    string   `json:"publisher"`
	Description  string   `json:"description,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Digest       string   `json:"digest"`        // hex sha256 of the archive bytes
	Archive      string   `json:"archive"`       // relative URL/path to the .tgz
	PublisherKey string   `json:"publisher_key"` // base64 ed25519 public key (shown, not auto-trusted)
}

// Index is a hub's package listing.
type Index struct {
	Packages []PackageEntry `json:"packages"`
}

// Find returns the entry for an id (highest version wins on duplicates is out of scope; first match).
func (idx Index) Find(id string) (PackageEntry, bool) {
	for _, p := range idx.Packages {
		if p.ID == id {
			return p, true
		}
	}
	return PackageEntry{}, false
}

// ArchiveDir builds a gzipped tar of a package directory and returns its bytes + hex sha256 digest.
func ArchiveDir(dir string) ([]byte, string, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		hdr := &tar.Header{Name: rel, Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	if err := tw.Close(); err != nil {
		return nil, "", err
	}
	if err := gz.Close(); err != nil {
		return nil, "", err
	}
	raw := buf.Bytes()
	sum := sha256.Sum256(raw)
	return raw, fmt.Sprintf("%x", sum), nil
}

// Extract unpacks a package archive into dstDir (creating it). It rejects path traversal.
func Extract(archive []byte, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(hdr.Name)
		if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
			return fmt.Errorf("hub: unsafe path in archive: %q", hdr.Name)
		}
		target := filepath.Join(dstDir, clean)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// VerifyDigest reports whether archive bytes match the expected hex sha256.
func VerifyDigest(archive []byte, wantHex string) bool {
	sum := sha256.Sum256(archive)
	return fmt.Sprintf("%x", sum) == wantHex
}

func marshalIndex(idx Index) ([]byte, error) { return json.MarshalIndent(idx, "", "  ") }
