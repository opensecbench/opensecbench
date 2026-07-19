package disposition

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func obs(attrs map[string]string, sev string) model.Observation {
	return model.Observation{Attributes: attrs, Severity: sev}
}

func TestEvaluate(t *testing.T) {
	rules := []Disposition{
		{When: map[string]string{"verified": "true"}, Action: ActionFinding},
		{When: map[string]string{"verified": "false"}, Action: ActionInvestigate},
	}
	if a := Evaluate(obs(map[string]string{"verified": "true"}, "high"), rules); a != ActionFinding {
		t.Fatalf("verified → %s, want finding", a)
	}
	if a := Evaluate(obs(map[string]string{"verified": "false"}, "medium"), rules); a != ActionInvestigate {
		t.Fatalf("unverified → %s, want investigate", a)
	}
	// No attribute → no match → default review.
	if a := Evaluate(obs(nil, "low"), rules); a != ActionReview {
		t.Fatalf("no match → %s, want review", a)
	}
}

func TestEvaluateFirstMatchWins(t *testing.T) {
	// An earlier (higher-priority) rule wins even if a later one also matches.
	rules := []Disposition{
		{Action: ActionReview}, // empty When matches anything
		{When: map[string]string{"verified": "true"}, Action: ActionFinding},
	}
	if a := Evaluate(obs(map[string]string{"verified": "true"}, "high"), rules); a != ActionReview {
		t.Fatalf("first rule should win, got %s", a)
	}
}

func TestEvaluateMinSeverity(t *testing.T) {
	rules := []Disposition{{MinSeverity: "high", Action: ActionFinding}}
	if a := Evaluate(obs(nil, "high"), rules); a != ActionFinding {
		t.Fatalf("high ≥ high should match, got %s", a)
	}
	if a := Evaluate(obs(nil, "medium"), rules); a != ActionReview {
		t.Fatalf("medium < high should not match, got %s", a)
	}
}
