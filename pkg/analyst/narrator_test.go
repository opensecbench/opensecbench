package analyst

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/report"
)

// The narrator's model reply (often wrapped in prose/code fences) is parsed and merged into the snapshot by
// finding id (ADR-0045).
func TestParseAndApplyNarrative(t *testing.T) {
	reply := "Sure, here is the report:\n```json\n" +
		`{"executive_summary":"The app has a critical auth flaw.","findings":[` +
		`{"id":"f1","impact":"Full account takeover.","remediation":"Enforce server-side authz."},` +
		`{"id":"f2","impact":"Info leak.","remediation":"Redact the field."}]}` +
		"\n```\nHope that helps!"

	n, err := parseNarrative(reply)
	if err != nil {
		t.Fatal(err)
	}
	if n.ExecutiveSummary != "The app has a critical auth flaw." || len(n.Findings) != 2 {
		t.Fatalf("parsed wrong: %+v", n)
	}

	data := report.Data{Findings: []report.Finding{
		{Finding: model.Finding{ID: "f1", Title: "Authz"}},
		{Finding: model.Finding{ID: "f2", Title: "Leak"}},
		{Finding: model.Finding{ID: "f3", Title: "Untouched"}},
	}}
	data.ApplyNarrative(n)
	if !data.Narrated || data.ExecutiveSummary == "" {
		t.Fatal("executive summary not applied")
	}
	if data.Findings[0].Impact != "Full account takeover." || data.Findings[1].Remediation != "Redact the field." {
		t.Fatalf("per-finding narrative not merged: %+v", data.Findings)
	}
	if data.Findings[2].Impact != "" {
		t.Fatal("finding without narrative should stay empty")
	}
}

func TestParseNarrativeNoJSON(t *testing.T) {
	if _, err := parseNarrative("I could not produce a report."); err == nil {
		t.Fatal("expected error when no JSON object present")
	}
}
