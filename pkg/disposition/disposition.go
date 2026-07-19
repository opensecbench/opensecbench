// Package disposition routes an interpreted observation to a post-run action (ADR-0028): promote it to a
// finding, open an investigation, or leave it for manual review. Rules are declared by a capability's
// manifest (tool-owned defaults) and overridden per project; this package is the pure matcher.
package disposition

import "github.com/opensecbench/opensecbench/pkg/model"

// Actions a disposition can take.
const (
	ActionFinding     = "finding"     // auto-confirm the observation and promote it to a finding
	ActionInvestigate = "investigate" // open a tracked investigation for a human/agent to validate
	ActionReview      = "review"      // leave as an unreviewed observation for manual triage (default)
)

// Disposition is one routing rule: an observation matching When (all keys equal) and at least MinSeverity
// gets Action. An empty When matches any observation; empty MinSeverity imposes no floor.
type Disposition struct {
	When        map[string]string `json:"when,omitempty"`
	MinSeverity string            `json:"min_severity,omitempty"`
	Action      string            `json:"action"`
}

// severityRank orders OSB severities; -1 for unknown so an unknown floor never blocks.
var severityRank = map[string]int{"info": 0, "low": 1, "medium": 2, "high": 3, "critical": 4}

// Evaluate returns the action for an observation: the first matching rule's action, else ActionReview.
// Rules are tried in order, so callers put higher-priority (e.g. project override) rules first.
func Evaluate(o model.Observation, rules []Disposition) string {
	for _, r := range rules {
		if matches(o, r) {
			if r.Action == "" {
				return ActionReview
			}
			return r.Action
		}
	}
	return ActionReview
}

func matches(o model.Observation, r Disposition) bool {
	for k, v := range r.When {
		if o.Attributes[k] != v {
			return false
		}
	}
	if r.MinSeverity != "" && severityRank[o.Severity] < severityRank[r.MinSeverity] {
		return false
	}
	return true
}
