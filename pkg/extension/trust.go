package extension

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// TrustStore holds trusted publisher public keys, one file per publisher under a directory.
type TrustStore struct {
	dir  string
	keys map[string]ed25519.PublicKey
}

// LoadTrustStore reads all <publisher>.pub keys from dir (created if missing).
func LoadTrustStore(dir string) (*TrustStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	ts := &TrustStore{dir: dir, keys: map[string]ed25519.PublicKey{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".pub" {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		key, err := decodeKey(string(trimSpace(raw)))
		if err != nil {
			continue
		}
		publisher := e.Name()[:len(e.Name())-len(".pub")]
		ts.keys[publisher] = key
	}
	return ts, nil
}

// Key returns the trusted public key for a publisher, or nil.
func (t *TrustStore) Key(publisher string) ed25519.PublicKey {
	if t == nil {
		return nil
	}
	return t.keys[publisher]
}

// Publishers lists trusted publisher names.
func (t *TrustStore) Publishers() []string {
	out := make([]string, 0, len(t.keys))
	for p := range t.keys {
		out = append(out, p)
	}
	return out
}

// Trust adds (or replaces) a publisher's trusted key (base64 ed25519 public key).
func (t *TrustStore) Trust(publisher, base64Key string) error {
	key, err := decodeKey(base64Key)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(t.dir, publisher+".pub"), []byte(base64Key), 0o600); err != nil {
		return err
	}
	t.keys[publisher] = key
	return nil
}

func decodeKey(b64 string) (ed25519.PublicKey, error) {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("extension: public key must be %d bytes", ed25519.PublicKeySize)
	}
	return ed25519.PublicKey(raw), nil
}

// GenerateKeyPair returns a new ed25519 key pair, base64-encoded.
func GenerateKeyPair() (pub, priv string, err error) {
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pubKey), base64.StdEncoding.EncodeToString(privKey), nil
}

// Sign produces a detached base64 ed25519 signature over a manifest's digest.
func Sign(m Manifest, base64PrivKey string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(base64PrivKey)
	if err != nil {
		return "", err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return "", fmt.Errorf("extension: private key must be %d bytes", ed25519.PrivateKeySize)
	}
	_, digest, err := Digest(m)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(ed25519.PrivateKey(raw), digest)
	return base64.StdEncoding.EncodeToString(sig), nil
}
