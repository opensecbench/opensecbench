package runnerhub

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"time"
)

// Runner request authentication (ADR-0024). A runner signs each request with the ed25519 private key it
// established at enrollment; the control plane verifies against the stored public key. The signature
// covers the method, path, a timestamp, and a hash of the body, so it can't be replayed to a different
// request, and a timestamp window bounds replay of the same one. Both sides build the signed string with
// CanonicalRequest so they always agree.

// Auth header names.
const (
	HeaderRunnerID = "X-OSB-Runner-Id"
	HeaderTime     = "X-OSB-Timestamp"
	HeaderSig      = "X-OSB-Runner-Sig"
)

// clockSkew bounds how far a request timestamp may be from now.
const clockSkew = 60 * time.Second

// CanonicalRequest is the exact string a runner signs and the server verifies.
func CanonicalRequest(method, path, timestamp string, body []byte) string {
	sum := sha256.Sum256(body)
	return method + "\n" + path + "\n" + timestamp + "\n" + hex.EncodeToString(sum[:])
}

// Sign produces the base64 signature for a request, given the runner's base64 ed25519 private key.
func Sign(privKeyB64, method, path, timestamp string, body []byte) (string, error) {
	priv, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return "", err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("runnerhub: bad private key size")
	}
	sig := ed25519.Sign(ed25519.PrivateKey(priv), []byte(CanonicalRequest(method, path, timestamp, body)))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks a request signature against the runner's base64 ed25519 public key, enforcing the
// timestamp window. Returns nil when authentic.
func Verify(pubKeyB64, method, path, timestamp, sigB64 string, body []byte, now time.Time) error {
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return errors.New("runnerhub: bad timestamp")
	}
	if d := now.Unix() - ts; d > int64(clockSkew.Seconds()) || d < -int64(clockSkew.Seconds()) {
		return errors.New("runnerhub: timestamp outside allowed window")
	}
	pub, err := base64.StdEncoding.DecodeString(pubKeyB64)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("runnerhub: bad public key")
	}
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return errors.New("runnerhub: bad signature encoding")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(CanonicalRequest(method, path, timestamp, body)), sig) {
		return errors.New("runnerhub: signature verification failed")
	}
	return nil
}

// GenerateKeyPair returns a fresh base64 ed25519 (publicKey, privateKey) pair for a runner.
func GenerateKeyPair() (pubB64, privB64 string, err error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(pub), base64.StdEncoding.EncodeToString(priv), nil
}

// TokenHash is the sha256 hex of an enrollment token — what the control plane stores (never the token).
func TokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
