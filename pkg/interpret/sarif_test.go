package interpret

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

const sampleSARIF = `{
  "version": "2.1.0",
  "runs": [
    {
      "tool": {"driver": {"name": "semgrep"}},
      "results": [
        {
          "ruleId": "python.lang.security.hardcoded-password",
          "level": "error",
          "message": {"text": "Hardcoded password detected.\nUse a secret manager."},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "app/config.py"}, "region": {"startLine": 12}}}
          ]
        },
        {
          "ruleId": "python.lang.correctness.unused",
          "level": "note",
          "message": {"text": "Unused import."},
          "locations": [
            {"physicalLocation": {"artifactLocation": {"uri": "app/util.py"}}}
          ]
        }
      ]
    }
  ]
}`

func TestSARIF(t *testing.T) {
	obs, err := SARIF([]byte(sampleSARIF))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2", len(obs))
	}

	first := obs[0]
	if first.Origin != model.OriginTool || first.ReviewState != model.ReviewUnreviewed {
		t.Fatalf("observation origin/state wrong: %+v", first)
	}
	if first.Title != "Hardcoded password detected." {
		t.Fatalf("title = %q", first.Title)
	}
	if first.Severity != "high" {
		t.Fatalf("severity = %q, want high (from level=error)", first.Severity)
	}
	if first.RuleID != "python.lang.security.hardcoded-password" {
		t.Fatalf("rule id = %q", first.RuleID)
	}
	if first.Location != "app/config.py:12" {
		t.Fatalf("location = %q, want app/config.py:12", first.Location)
	}

	if obs[1].Severity != "low" {
		t.Fatalf("second severity = %q, want low (from level=note)", obs[1].Severity)
	}
	if obs[1].Location != "app/util.py" {
		t.Fatalf("second location = %q (no line)", obs[1].Location)
	}
}

func TestSARIFSemgrepAttributes(t *testing.T) {
	obs, err := SARIF([]byte(sampleSARIF))
	if err != nil {
		t.Fatal(err)
	}
	// The driver name is carried as a routable attribute even without security-severity.
	if got := obs[0].Attributes["tool"]; got != "semgrep" {
		t.Fatalf("tool attribute = %q, want semgrep", got)
	}
	if _, ok := obs[0].Attributes["security_severity"]; ok {
		t.Fatalf("no security-severity in sample, should be absent")
	}
}

// grype emits security-severity (a CVSS base score) either on the rule definition or the result, while its
// SARIF level collapses Critical and High to "error". The interpreter must surface the score as an
// attribute and refine severity from it so MinSeverity routing can tell critical from high.
const grypeSARIF = `{
  "runs": [
    {
      "tool": {"driver": {"name": "grype", "rules": [
        {"id": "CVE-2021-CRIT", "properties": {"security-severity": "9.8"}}
      ]}},
      "results": [
        {
          "ruleId": "CVE-2021-CRIT",
          "level": "error",
          "message": {"text": "Critical vuln in libfoo 1.2.3"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "go.mod"}, "region": {"startLine": 5}}}]
        },
        {
          "ruleId": "CVE-2020-HIGH",
          "level": "error",
          "message": {"text": "High vuln in libbar 4.5.6"},
          "properties": {"security-severity": "7.5"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "go.mod"}, "region": {"startLine": 9}}}]
        }
      ]
    }
  ]
}`

func TestSARIFGrypeSecuritySeverity(t *testing.T) {
	obs, err := SARIF([]byte(grypeSARIF))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2", len(obs))
	}
	// Rule-level security-severity 9.8 → critical (not "high" from level=error).
	if obs[0].Severity != "critical" {
		t.Fatalf("first severity = %q, want critical (CVSS 9.8)", obs[0].Severity)
	}
	if got := obs[0].Attributes["security_severity"]; got != "9.8" {
		t.Fatalf("first security_severity = %q, want 9.8", got)
	}
	if obs[0].Attributes["tool"] != "grype" {
		t.Fatalf("first tool = %q, want grype", obs[0].Attributes["tool"])
	}
	// Result-level security-severity 7.5 → high.
	if obs[1].Severity != "high" {
		t.Fatalf("second severity = %q, want high (CVSS 7.5)", obs[1].Severity)
	}
	if got := obs[1].Attributes["security_severity"]; got != "7.5" {
		t.Fatalf("second security_severity = %q, want 7.5", got)
	}
}

// SCA dependency facts (ADR-0069). grype encodes the coordinate in fullDescription and Package/Version/Fix
// Version in the rule help text ("UNKNOWN" version dropped); osv states "group:artifact@version" in the
// result message and has no fix version. Shapes mirror real grype/osv SARIF output.
const grypeSCASARIF = `{
  "runs": [{
    "tool": {"driver": {"name": "grype", "rules": [
      {"id": "GHSA-x-postgresql", "properties": {"security-severity": "10.0"},
       "fullDescription": {"text": "org.postgresql:postgresql vulnerable to SQL Injection"},
       "help": {"text": "Vulnerability GHSA-x\nSeverity: critical\nPackage: postgresql\nVersion: UNKNOWN\nFix Version: 42.2.28\nType: java-archive"}}
    ]}},
    "results": [
      {"ruleId": "GHSA-x-postgresql", "message": {"text": "A critical vulnerability in java-archive package: postgresql, version UNKNOWN"},
       "locations": [{"physicalLocation": {"artifactLocation": {"uri": "/pom.xml"}}}]}
    ]
  }]
}`

const osvSCASARIF = `{
  "runs": [{
    "tool": {"driver": {"name": "osv-scanner", "rules": [{"id": "CVE-2022-45868"}]}},
    "results": [
      {"ruleId": "CVE-2022-45868", "level": "warning",
       "message": {"text": "Package 'com.h2database:h2@1.4.200' is vulnerable to 'CVE-2022-45868' (also known as 'GHSA-22wj-vf5f-wrvj')."},
       "locations": [{"physicalLocation": {"artifactLocation": {"uri": "file:///src/pom.xml"}}}]}
    ]
  }]
}`

func TestSARIFSCAFacts(t *testing.T) {
	g, err := SARIF([]byte(grypeSCASARIF))
	if err != nil || len(g) != 1 {
		t.Fatalf("grype: %d obs, err=%v", len(g), err)
	}
	// Coordinate from fullDescription; fix from help; "UNKNOWN" version dropped.
	if a := g[0].Attributes; a["package"] != "org.postgresql:postgresql" || a["version"] != "" || a["fixed_version"] != "42.2.28" {
		t.Fatalf("grype SCA attrs = %+v", g[0].Attributes)
	}

	o, err := SARIF([]byte(osvSCASARIF))
	if err != nil || len(o) != 1 {
		t.Fatalf("osv: %d obs, err=%v", len(o), err)
	}
	// Coordinate + version from the message; no fix version.
	if a := o[0].Attributes; a["package"] != "com.h2database:h2" || a["version"] != "1.4.200" || a["fixed_version"] != "" {
		t.Fatalf("osv SCA attrs = %+v", o[0].Attributes)
	}
}

// A semgrep taint finding carries a codeFlows dataflow trace (source → sink); a plain pattern finding does
// not. The interpreter marks the taint finding reachable and records where untrusted input enters (ADR-0032).
const taintSARIF = `{
  "runs": [
    {
      "tool": {"driver": {"name": "semgrep"}},
      "results": [
        {
          "ruleId": "python.django.security.injection.sql",
          "level": "error",
          "message": {"text": "SQL injection from user input"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "app/views.py"}, "region": {"startLine": 42}}}],
          "codeFlows": [{"threadFlows": [{"locations": [
            {"location": {"physicalLocation": {"artifactLocation": {"uri": "app/views.py"}, "region": {"startLine": 12}}}},
            {"location": {"physicalLocation": {"artifactLocation": {"uri": "app/views.py"}, "region": {"startLine": 42}}}}
          ]}]}]
        },
        {
          "ruleId": "python.lang.security.hardcoded-password",
          "level": "error",
          "message": {"text": "Hardcoded password"},
          "locations": [{"physicalLocation": {"artifactLocation": {"uri": "app/config.py"}, "region": {"startLine": 5}}}]
        }
      ]
    }
  ]
}`

func TestSARIFDataflowReachability(t *testing.T) {
	obs, err := SARIF([]byte(taintSARIF))
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 {
		t.Fatalf("got %d observations, want 2", len(obs))
	}
	// The taint finding is reachable, with the dataflow source (where input enters) captured.
	taint := obs[0]
	if taint.Attributes["reachable"] != "true" {
		t.Fatalf("taint finding reachable = %q, want true", taint.Attributes["reachable"])
	}
	if taint.Attributes["dataflow_source"] != "app/views.py:12" {
		t.Fatalf("dataflow_source = %q, want app/views.py:12", taint.Attributes["dataflow_source"])
	}
	// The full source→sink path is captured so the engine can test route→sink reachability against it.
	if taint.Attributes["dataflow_path"] != "app/views.py:12,app/views.py:42" {
		t.Fatalf("dataflow_path = %q, want app/views.py:12,app/views.py:42", taint.Attributes["dataflow_path"])
	}
	// The plain pattern finding has no dataflow trace → reachability not applicable (unset, not "false").
	if _, has := obs[1].Attributes["reachable"]; has {
		t.Fatalf("pattern finding should have no reachable attribute, got %q", obs[1].Attributes["reachable"])
	}
}

func TestSARIFBadJSON(t *testing.T) {
	if _, err := SARIF([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid SARIF")
	}
}
