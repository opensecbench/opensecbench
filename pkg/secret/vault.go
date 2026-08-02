// Package secret is the encrypted vault (ADR-0011): values are sealed with AES-256-GCM and
// referenced by name. Plaintext leaves the vault only into a sandboxed runner at exec time or a
// governed integration call — never through the API or into logs.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// KeySize is the AES-256 key length.
const KeySize = 32

// Vault seals and opens secret values with a symmetric key.
type Vault struct {
	aead cipher.AEAD
}

// NewVault builds a vault from a 32-byte key.
func NewVault(key []byte) (*Vault, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secret: key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Vault{aead: aead}, nil
}

// LoadVault resolves the instance-wide master key (OSB_VAULT_KEY base64, else a 0600 key file in dir,
// generated on first use) and returns a Vault. See ADR-0011 for the key-custody tradeoff. Per-project
// vaults use LoadVaultDir instead so each project stays self-contained (ADR-0049).
func LoadVault(dir string) (*Vault, error) {
	if env := os.Getenv("OSB_VAULT_KEY"); env != "" {
		key, err := base64.StdEncoding.DecodeString(env)
		if err != nil {
			return nil, fmt.Errorf("secret: OSB_VAULT_KEY is not valid base64: %w", err)
		}
		return NewVault(key)
	}
	return LoadVaultDir(dir)
}

// LoadVaultDir loads the vault whose key file (vault.key) lives in dir, generating it 0600 on first use.
// Unlike LoadVault it never consults OSB_VAULT_KEY, so a project vault is keyed only by its own directory
// and its secrets do not silently share the instance master key when that env var is set.
func LoadVaultDir(dir string) (*Vault, error) {
	keyPath := filepath.Join(dir, "vault.key")
	if raw, err := os.ReadFile(keyPath); err == nil {
		key := make([]byte, base64.StdEncoding.DecodedLen(len(raw)))
		n, derr := base64.StdEncoding.Decode(key, raw)
		if derr != nil {
			return nil, fmt.Errorf("secret: vault.key is corrupt: %w", derr)
		}
		return NewVault(key[:n])
	}
	// Generate and persist a new key (0600).
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	enc := base64.StdEncoding.EncodeToString(key)
	if err := os.WriteFile(keyPath, []byte(enc), 0o600); err != nil {
		return nil, err
	}
	return NewVault(key)
}

// Seal encrypts plaintext, returning a base64 blob (nonce || ciphertext).
func (v *Vault) Seal(plaintext []byte) (string, error) {
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := v.aead.Seal(nonce, nonce, plaintext, nil)
	return base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a blob produced by Seal.
func (v *Vault) Open(blob string) ([]byte, error) {
	raw, err := base64.StdEncoding.DecodeString(blob)
	if err != nil {
		return nil, err
	}
	ns := v.aead.NonceSize()
	if len(raw) < ns {
		return nil, errors.New("secret: ciphertext too short")
	}
	nonce, ct := raw[:ns], raw[ns:]
	return v.aead.Open(nil, nonce, ct, nil)
}
