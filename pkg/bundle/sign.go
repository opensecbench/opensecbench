package bundle

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// Sidecar is a detached signature over a bundle's bytes, attributing it to a publisher (ADR-0012).
// It travels alongside the bundle (e.g. <bundle>.sig) so a receiver can verify authorship.
type Sidecar struct {
	Publisher string `json:"publisher"`
	PublicKey string `json:"public_key"` // base64 ed25519 public key
	Signature string `json:"signature"`  // base64 ed25519 signature over sha256(bundle)
}

// Sign produces a sidecar for bundle bytes using a base64 ed25519 private key.
func Sign(bundle []byte, publisher, privB64 string) (Sidecar, error) {
	raw, err := base64.StdEncoding.DecodeString(privB64)
	if err != nil {
		return Sidecar{}, err
	}
	if len(raw) != ed25519.PrivateKeySize {
		return Sidecar{}, fmt.Errorf("bundle: private key must be %d bytes", ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)
	sum := sha256.Sum256(bundle)
	sig := ed25519.Sign(priv, sum[:])
	pub := priv.Public().(ed25519.PublicKey)
	return Sidecar{
		Publisher: publisher,
		PublicKey: base64.StdEncoding.EncodeToString(pub),
		Signature: base64.StdEncoding.EncodeToString(sig),
	}, nil
}

// Verify checks a sidecar's signature over the bundle bytes. It proves the holder of PublicKey
// signed the bundle; whether to trust that key is the operator's decision.
func (s Sidecar) Verify(bundle []byte) error {
	pub, err := base64.StdEncoding.DecodeString(s.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("bundle: bad public key in sidecar")
	}
	sig, err := base64.StdEncoding.DecodeString(s.Signature)
	if err != nil {
		return fmt.Errorf("bundle: bad signature encoding")
	}
	sum := sha256.Sum256(bundle)
	if !ed25519.Verify(pub, sum[:], sig) {
		return fmt.Errorf("bundle: signature does not verify (wrong key or tampered bundle)")
	}
	return nil
}

// MarshalSidecar / ParseSidecar serialize the sidecar as JSON.
func MarshalSidecar(s Sidecar) ([]byte, error) { return json.MarshalIndent(s, "", "  ") }

// ParseSidecar parses a sidecar JSON.
func ParseSidecar(data []byte) (Sidecar, error) {
	var s Sidecar
	err := json.Unmarshal(data, &s)
	return s, err
}
