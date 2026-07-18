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

func TestSARIFBadJSON(t *testing.T) {
	if _, err := SARIF([]byte("not json")); err == nil {
		t.Fatal("expected error for invalid SARIF")
	}
}
