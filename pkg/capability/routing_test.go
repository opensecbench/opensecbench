package capability

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func obs(sev string, attrs map[string]string) model.Observation {
	return model.Observation{Severity: sev, Attributes: attrs}
}

// The route gate escalates a finding on a traffic-confirmed exposed route, without ever downgrading on the
// absence of a route or a reachability signal (ADR-0034).
func TestSastRouting(t *testing.T) {
	cases := []struct {
		name string
		o    model.Observation
		want string
	}{
		{"medium on confirmed route", obs("medium", map[string]string{"route_observed": "true"}), disposition.ActionInvestigate},
		{"low on confirmed route stays review", obs("low", map[string]string{"route_observed": "true"}), disposition.ActionReview},
		{"reachable taint any severity", obs("low", map[string]string{"reachable": "true", "exposed": "true"}), disposition.ActionInvestigate},
		{"high pattern fallback", obs("high", nil), disposition.ActionInvestigate},
		{"medium pattern, no route", obs("medium", nil), disposition.ActionReview},
	}
	for _, c := range cases {
		if got := disposition.Evaluate(c.o, sastReachabilityRouting); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestScaRouting(t *testing.T) {
	cases := []struct {
		name string
		o    model.Observation
		want string
	}{
		// reachable:false is authoritative — even on a live route and rated high, it stays review.
		{"uncalled on confirmed route", obs("high", map[string]string{"reachable": "false", "route_observed": "true"}), disposition.ActionReview},
		{"medium on confirmed route", obs("medium", map[string]string{"route_observed": "true"}), disposition.ActionInvestigate},
		{"low on confirmed route stays review", obs("low", map[string]string{"route_observed": "true"}), disposition.ActionReview},
		{"reachable+exposed", obs("low", map[string]string{"reachable": "true", "exposed": "true"}), disposition.ActionInvestigate},
		{"high no verdict fallback", obs("high", nil), disposition.ActionInvestigate},
		{"medium no verdict, no route", obs("medium", nil), disposition.ActionReview},
	}
	for _, c := range cases {
		if got := disposition.Evaluate(c.o, scaReachabilityRouting); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}
