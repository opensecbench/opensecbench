// Package cas is a content-addressed store for immutable artifacts, keyed by SHA-256.
//
// Artifacts are the immutable roots of the provenance chain (ADR-0002): identical content
// always yields the same digest and on-disk location, and stored content is never mutated.
package cas

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// Store persists artifacts under a root directory, addressed by hex SHA-256 digest.
type Store struct {
	root string
}

// Open prepares a content-addressed store rooted at dir, creating it if needed.
func Open(dir string) (*Store, error) {
	if dir == "" {
		return nil, errors.New("cas: empty root directory")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

// Put stores everything read from r and returns its digest. It is idempotent: storing
// identical content again returns the same digest without rewriting the artifact.
func (s *Store) Put(r io.Reader) (digest string, err error) {
	tmp, err := os.CreateTemp(s.root, ".tmp-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), r); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}

	digest = hex.EncodeToString(h.Sum(nil))
	dst := s.path(digest)
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil {
		return digest, nil // already stored
	}
	if err := os.Rename(tmp.Name(), dst); err != nil {
		return "", err
	}
	return digest, nil
}

// Open returns a reader for the artifact with the given digest.
func (s *Store) Open(digest string) (io.ReadCloser, error) {
	if len(digest) < 2 {
		return nil, errors.New("cas: invalid digest")
	}
	return os.Open(s.path(digest))
}

// path fans artifacts out by the first two hex characters to keep directories shallow.
func (s *Store) path(digest string) string {
	return filepath.Join(s.root, digest[:2], digest)
}
