package runnerhub

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"
)

// Runner request authentication (ADR-0024). A runner signs each request with the ed25519 private key it
// established at enrollment; the control plane verifies against the stored public key. The signature
// covers the method, path, a timestamp, a per-request nonce, and a hash of the body, so it can't be
// replayed to a different request. A timestamp window bounds how long any signature is valid, and a
// server-side ReplayGuard rejects a nonce seen twice inside that window, so even the same request cannot
// be replayed. Both sides build the signed string with CanonicalRequest so they always agree.

// Auth header names.
const (
	HeaderRunnerID = "X-OSB-Runner-Id"
	HeaderTime     = "X-OSB-Timestamp"
	HeaderSig      = "X-OSB-Runner-Sig"
	HeaderNonce    = "X-OSB-Runner-Nonce"
)

// clockSkew bounds how far a request timestamp may be from now.
const clockSkew = 60 * time.Second

// Nonce returns a fresh random nonce for a request (base64, 128 bits).
func Nonce() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(b[:]), nil
}

// CanonicalRequest is the exact string a runner signs and the server verifies.
func CanonicalRequest(method, path, timestamp, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + hex.EncodeToString(sum[:])
}

// Sign produces the base64 signature for a request, given the runner's base64 ed25519 private key.
func Sign(privKeyB64, method, path, timestamp, nonce string, body []byte) (string, error) {
	priv, err := base64.StdEncoding.DecodeString(privKeyB64)
	if err != nil {
		return "", err
	}
	if len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("runnerhub: bad private key size")
	}
	sig := ed25519.Sign(ed25519.PrivateKey(priv), []byte(CanonicalRequest(method, path, timestamp, nonce, body)))
	return base64.StdEncoding.EncodeToString(sig), nil
}

// Verify checks a request signature against the runner's base64 ed25519 public key, enforcing the
// timestamp window and requiring a nonce. Returns nil when authentic. Replay of the same signed request
// within the window is caught separately by ReplayGuard, which needs the runner id to scope nonces.
func Verify(pubKeyB64, method, path, timestamp, nonce, sigB64 string, body []byte, now time.Time) error {
	if nonce == "" {
		return errors.New("runnerhub: missing nonce")
	}
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
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(CanonicalRequest(method, path, timestamp, nonce, body)), sig) {
		return errors.New("runnerhub: signature verification failed")
	}
	return nil
}

// ReplayGuard remembers recently seen (runner, nonce) pairs so a captured request signature cannot be
// replayed inside the timestamp window. Entries expire after 2*clockSkew — the full real-time span over
// which any single signature can still pass Verify's timestamp check — so the map stays bounded to live
// traffic. Safe for concurrent use.
type ReplayGuard struct {
	mu   sync.Mutex
	seen map[string]int64 // runnerID+"\n"+nonce -> unix expiry
}

// NewReplayGuard builds an empty guard.
func NewReplayGuard() *ReplayGuard {
	return &ReplayGuard{seen: map[string]int64{}}
}

// Check records (runnerID, nonce) and reports whether it is fresh. It returns false when the pair was
// already seen inside the retention window — i.e. a replay. Callers should reject on false.
func (g *ReplayGuard) Check(runnerID, nonce string, now time.Time) bool {
	key := runnerID + "\n" + nonce
	exp := now.Add(2 * clockSkew).Unix()
	g.mu.Lock()
	defer g.mu.Unlock()
	nowUnix := now.Unix()
	for k, e := range g.seen { // opportunistic trim of expired entries
		if e <= nowUnix {
			delete(g.seen, k)
		}
	}
	if _, ok := g.seen[key]; ok {
		return false
	}
	g.seen[key] = exp
	return true
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
