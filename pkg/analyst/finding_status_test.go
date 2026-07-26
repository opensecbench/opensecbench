package analyst

import "testing"

// set_finding_status is capability parity with the human's finding dropdown (ADR-0053): the agent has the
// tool, and it is NOT gated by default — like triage_observation, not create_finding. Governance is layered
// on via the approval policy, not hardcoded here.
func TestSetFindingStatusInCatalogAndNotGated(t *testing.T) {
	found, hasEnum := false, false
	for _, tl := range Tools() {
		if tl.Name != "set_finding_status" {
			continue
		}
		found = true
		for _, p := range tl.Params {
			if p.Name == "status" && len(p.Enum) > 0 {
				hasEnum = true
			}
		}
	}
	if !found {
		t.Fatal("set_finding_status missing from catalog")
	}
	if !hasEnum {
		t.Fatal("set_finding_status needs a status enum")
	}
	if sensitiveTools["set_finding_status"] {
		t.Fatal("set_finding_status should not be gated by default (capability parity with the human dropdown)")
	}
}
