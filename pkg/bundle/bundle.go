// Package bundle exports and imports a portable, encrypted project bundle (ADR-0012): the shareable
// assessment graph (findings + evidence + KB) plus its CAS blobs, sealed with a passphrase. It is
// the offline, peer-to-peer primitive for sharing and backup.
package bundle

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/scrypt"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// FormatVersion is the bundle schema version. v2 adds the optional full-fidelity working state
// (ADR-0060); a pre-v2 daemon rejects a v2 bundle, a v2 daemon reads a v1 bundle (new slices empty).
const FormatVersion = 2

var magic = []byte("OSBBNDL1")

const (
	saltLen  = 16
	scryptN  = 1 << 15
	scryptR  = 8
	scryptP  = 1
	keyLen   = 32
	nonceLen = 12
)

// Data is the decrypted, serializable content of a bundle.
type Data struct {
	Version      int                 `json:"version"`
	Project      model.Project       `json:"project"`
	Targets      []model.Target      `json:"targets"`
	Applications []model.Application `json:"applications"`
	Assets       []model.Asset       `json:"assets"`
	Scope        []model.ScopeEntry  `json:"scope"`
	Findings     []model.Finding     `json:"findings"` // ObservationIDs populated
	Observations []model.Observation `json:"observations"`
	Artifacts    []model.Artifact    `json:"artifacts"`
	KB           []model.KBEntry     `json:"kb"`

	// Full-fidelity working state (mode=full, ADR-0060) — the state a demo/backup needs but a
	// client-facing deliverable does not. Empty/nil in a shareable bundle.
	Threads        []model.Thread        `json:"threads,omitempty"`
	Messages       []model.Message       `json:"messages,omitempty"`
	Investigations []model.Investigation `json:"investigations,omitempty"`
	Exchanges      []model.HTTPExchange  `json:"exchanges,omitempty"`
	Reports        []model.Report        `json:"reports,omitempty"`
	ContextItems   []model.ContextItem   `json:"context_items,omitempty"`
	Adopted        []string              `json:"adopted_methodologies,omitempty"`
	Coverage       []model.CoverageEntry `json:"coverage,omitempty"`
	Engagement     *model.Engagement     `json:"engagement,omitempty"`

	Blobs map[string][]byte `json:"blobs"` // sha256 -> bytes
}

// seal encrypts the JSON of d with a scrypt-derived key from passphrase. Layout:
// magic | salt(16) | nonce(12) | ciphertext.
func seal(d *Data, passphrase string) ([]byte, error) {
	if passphrase == "" {
		return nil, errors.New("bundle: passphrase required")
	}
	plain, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, saltLen)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, err
	}
	aead, err := aeadFrom(passphrase, salt)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, nonceLen)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plain, magic)

	out := make([]byte, 0, len(magic)+saltLen+nonceLen+len(ct))
	out = append(out, magic...)
	out = append(out, salt...)
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// open decrypts a bundle produced by seal.
func open(blob []byte, passphrase string) (*Data, error) {
	hdr := len(magic) + saltLen + nonceLen
	if len(blob) < hdr {
		return nil, errors.New("bundle: file too short")
	}
	if string(blob[:len(magic)]) != string(magic) {
		return nil, errors.New("bundle: bad magic (not an OpenSecBench bundle)")
	}
	salt := blob[len(magic) : len(magic)+saltLen]
	nonce := blob[len(magic)+saltLen : hdr]
	ct := blob[hdr:]

	aead, err := aeadFrom(passphrase, salt)
	if err != nil {
		return nil, err
	}
	plain, err := aead.Open(nil, nonce, ct, magic)
	if err != nil {
		return nil, fmt.Errorf("bundle: decrypt failed (wrong passphrase or corrupt file)")
	}
	var d Data
	if err := json.Unmarshal(plain, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func aeadFrom(passphrase string, salt []byte) (cipher.AEAD, error) {
	key, err := scrypt.Key([]byte(passphrase), salt, scryptN, scryptR, scryptP, keyLen)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
