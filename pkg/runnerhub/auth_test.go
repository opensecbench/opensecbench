package runnerhub

import (
	"testing"
	"time"
)

func TestReplayGuardRejectsReuse(t *testing.T) {
	g := NewReplayGuard()
	now := time.Unix(1_700_000_000, 0)
	if !g.Check("r1", "n1", now) {
		t.Fatal("first use should be fresh")
	}
	if g.Check("r1", "n1", now) {
		t.Fatal("second use of same nonce must be rejected")
	}
	// Same nonce, different runner is independent.
	if !g.Check("r2", "n1", now) {
		t.Fatal("same nonce under a different runner should be fresh")
	}
	// A nonce whose retention window has passed is forgotten and accepted again.
	later := now.Add(2*clockSkew + time.Second)
	if !g.Check("r1", "n1", later) {
		t.Fatal("expired nonce should be accepted again")
	}
}

func TestVerifyRequiresNonce(t *testing.T) {
	pub, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	ts := "1700000000"
	sig, err := Sign(priv, "GET", "/v1/runners/stream", ts, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Even a correctly computed signature over an empty nonce is refused.
	if err := Verify(pub, "GET", "/v1/runners/stream", ts, "", sig, nil, now); err == nil {
		t.Fatal("empty nonce must be rejected")
	}
}
