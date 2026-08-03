package analyst

import (
	"sort"
	"testing"
)

// has reports whether s contains v.
func hasStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// The pentester profile carries sensitive tools (run_code, send_request, run_capability) plus reversible
// reads. delegateAuthorization must narrow what a sub-agent auto-runs by the human's policy.
func TestDelegateAuthorizationNarrowsByPolicy(t *testing.T) {
	prof := ProfileByID("pentester")
	if len(prof.ToolSet()) == 0 {
		t.Fatal("pentester profile has no tools; test premise broken")
	}
	sensitive := []string{"run_code", "send_request", "run_capability"}
	for _, s := range sensitive {
		if !hasStr(profileToolNames(prof), s) {
			t.Fatalf("premise: pentester should hold %q", s)
		}
	}

	cautious := DefaultPolicy() // Execute/External confirm

	// Unattended delegation (no human approval) under Cautious: sensitive tools are WITHHELD, so an
	// injected/scheduled agent can't reach them by delegating.
	unattended := delegateAuthorization(prof, cautious, false)
	for _, s := range sensitive {
		if hasStr(unattended, s) {
			t.Errorf("unattended cautious delegation must not authorize %q", s)
		}
	}
	// Reversible reads still flow.
	if !hasStr(unattended, "get_finding") && !hasStr(unattended, "read_context") {
		t.Errorf("reversible reads should remain authorized, got %v", unattended)
	}

	// Human-approved delegation under Cautious: the explicit approval restores the sensitive toolset.
	approved := delegateAuthorization(prof, cautious, true)
	for _, s := range sensitive {
		if !hasStr(approved, s) {
			t.Errorf("human-approved delegation should authorize %q", s)
		}
	}

	// Trusted autonomy: Execute/External run free, so even unattended delegation authorizes everything —
	// consistent with the human's chosen envelope (delegation grants nothing beyond direct use).
	trusted := cautious.WithAutonomy(AutonomyTrusted)
	trustedAuthz := delegateAuthorization(prof, trusted, false)
	if len(trustedAuthz) != len(profileToolNames(prof)) {
		t.Errorf("trusted autonomy should authorize the whole profile toolset: got %d of %d",
			len(trustedAuthz), len(profileToolNames(prof)))
	}
}

// delegateGatedTools surfaces exactly the sensitive capabilities a human authorizes by approving the
// delegation — the "informed" half of the consent.
func TestDelegateGatedToolsDisclosure(t *testing.T) {
	prof := ProfileByID("pentester")
	got := delegateGatedTools(prof, DefaultPolicy())
	sort.Strings(got)
	for _, want := range []string{"run_capability", "run_code", "send_request"} {
		if !hasStr(got, want) {
			t.Errorf("disclosure should include %q; got %v", want, got)
		}
	}
	// A purely reversible tool must not appear in the disclosure.
	if hasStr(got, "get_finding") {
		t.Errorf("reversible tools must not be disclosed as authorized-sensitive: %v", got)
	}
	// Under Trusted nothing is gated, so the disclosure is empty (the human already runs these free).
	if d := delegateGatedTools(prof, DefaultPolicy().WithAutonomy(AutonomyTrusted)); len(d) != 0 {
		t.Errorf("trusted autonomy gates nothing; disclosure should be empty, got %v", d)
	}
}
