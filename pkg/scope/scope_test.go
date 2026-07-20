package scope

import "testing"

func TestCheck(t *testing.T) {
	entries := []Entry{
		{Kind: KindHost, Value: "api.acme.com"},
		{Kind: KindDomain, Value: "acme.io"},
		{Kind: KindCIDR, Value: "10.0.0.0/24"},
	}

	inScope := []string{
		"api.acme.com",
		"https://api.acme.com/v2/x",
		"api.acme.com:443",
		"acme.io",
		"www.acme.io",
		"deep.sub.acme.io",
		"10.0.0.5",
		"http://10.0.0.200:8080",
	}
	for _, target := range inScope {
		if err := Check(entries, target); err != nil {
			t.Errorf("Check(%q) = %v, want in scope", target, err)
		}
	}

	outOfScope := []string{
		"evil.com",
		"acme.com", // not acme.io, and no host entry
		"notacme.io.evil.com",
		"10.0.1.5", // outside the /24
		"api.acme.com.evil.com",
	}
	for _, target := range outOfScope {
		if err := Check(entries, target); err == nil {
			t.Errorf("Check(%q) = nil, want out of scope", target)
		}
	}
}

func TestCheckEmptyScopeAllows(t *testing.T) {
	if err := Check(nil, "anything.com"); err != nil {
		t.Fatalf("empty scope should allow, got %v", err)
	}
}

// TestDenyPrecedence verifies out-of-scope (deny) entries win over allow entries (ADR-0051).
func TestDenyPrecedence(t *testing.T) {
	entries := []Entry{
		{Kind: KindDomain, Value: "acme.com", Disposition: Allow},
		{Kind: KindHost, Value: "payments.acme.com", Disposition: Deny},
		{Kind: KindDomain, Value: "corp.acme.com", Disposition: Deny},
	}
	// Denied even though the allow domain would otherwise match.
	for _, target := range []string{"payments.acme.com", "https://payments.acme.com/x", "vpn.corp.acme.com"} {
		if err := Check(entries, target); err == nil {
			t.Errorf("Check(%q) = nil, want out-of-scope (deny wins)", target)
		}
	}
	// Still in scope via the allow domain.
	for _, target := range []string{"shop.acme.com", "acme.com"} {
		if err := Check(entries, target); err != nil {
			t.Errorf("Check(%q) = %v, want in scope", target, err)
		}
	}
	// Deny-only list: allow-all except the excluded host (no allow entries = allow-all minus denies).
	denyOnly := []Entry{{Kind: KindHost, Value: "prod.acme.com", Disposition: Deny}}
	if err := Check(denyOnly, "prod.acme.com"); err == nil {
		t.Error("deny-only: excluded host should be blocked")
	}
	if err := Check(denyOnly, "anything-else.com"); err != nil {
		t.Errorf("deny-only: non-excluded target should be allowed, got %v", err)
	}
}
