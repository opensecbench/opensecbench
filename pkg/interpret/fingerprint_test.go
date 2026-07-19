package interpret

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestFingerprintStableAndDistinct(t *testing.T) {
	base := model.Observation{
		Origin: model.OriginTool, RuleID: "CVE-2021-CRIT", Location: "go.mod:5", Detail: "Critical vuln",
	}
	fp := Fingerprint(base)
	if fp == "" {
		t.Fatal("fingerprint should not be empty")
	}
	// Same content → same fingerprint (a re-scan is recognised as the same finding).
	if again := Fingerprint(base); again != fp {
		t.Fatalf("fingerprint not stable: %q vs %q", fp, again)
	}
	// Severity and attributes are excluded — a CVSS/verified shift is still the same finding.
	shifted := base
	shifted.Severity = "high"
	shifted.Attributes = map[string]string{"security_severity": "8.0"}
	if Fingerprint(shifted) != fp {
		t.Fatal("severity/attributes must not affect the fingerprint")
	}
	// A different rule or location is a different finding.
	for _, diff := range []model.Observation{
		{Origin: model.OriginTool, RuleID: "CVE-2020-HIGH", Location: "go.mod:5", Detail: "Critical vuln"},
		{Origin: model.OriginTool, RuleID: "CVE-2021-CRIT", Location: "go.mod:9", Detail: "Critical vuln"},
		{Origin: model.OriginTool, RuleID: "CVE-2021-CRIT", Location: "go.mod:5", Detail: "Different detail"},
	} {
		if Fingerprint(diff) == fp {
			t.Fatalf("distinct finding hashed the same: %+v", diff)
		}
	}
}
