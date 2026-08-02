package controlplane

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// apiTokenFile is the basename of the local API bearer token, stored beside the database in the data
// dir (ADR-0061). Filesystem ownership (0600) is the authentication: any client that can read it is
// already running as the daemon's user.
const apiTokenFile = "api-token"

// APITokenPath returns the on-disk location of the local API token for a given data dir. Clients (the
// osb CLI, any future local client) read this file to authenticate to the loopback API.
func APITokenPath(dir string) string { return filepath.Join(dir, apiTokenFile) }

// ReadAPIToken returns the local API token for a data dir WITHOUT creating one — for clients that must
// use a running daemon's token and never mint a fresh one: the osb CLI, a future CLI/TUI, or the desktop
// app attaching to an external daemon. Returns ("", nil) when the file is absent.
func ReadAPIToken(dir string) (string, error) {
	b, err := os.ReadFile(APITokenPath(dir))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read api token: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

// LoadOrCreateAPIToken returns the persistent local API token for the data dir, creating it on first
// use. The token is 32 bytes of crypto/rand, hex-encoded, written 0600 via a temp-file-and-rename so
// it is never briefly world-readable. It is reused across restarts (ADR-0061: persistent token);
// rotate it by deleting the file.
func LoadOrCreateAPIToken(dir string) (string, error) {
	path := APITokenPath(dir)
	if b, err := os.ReadFile(path); err == nil {
		if tok := strings.TrimSpace(string(b)); tok != "" {
			return tok, nil
		}
		// Empty/corrupt file: fall through and regenerate.
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read api token: %w", err)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	tok := hex.EncodeToString(raw)

	// Temp-file-and-rename so the token is never observable at 0644 mid-write.
	tmp, err := os.CreateTemp(dir, apiTokenFile+".*")
	if err != nil {
		return "", fmt.Errorf("create api token: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("chmod api token: %w", err)
	}
	if _, err := tmp.WriteString(tok); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("write api token: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close api token: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("install api token: %w", err)
	}
	return tok, nil
}
