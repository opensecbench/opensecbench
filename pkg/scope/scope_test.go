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
