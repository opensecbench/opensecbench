package capability

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/disposition"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func obs(sev string, attrs map[string]string) model.Observation {
	return model.Observation{Severity: sev, Attributes: attrs}
}

// Queue-first triage (ADR-0068): SCA/SAST scanners declare NO dispositions, so their observations land in the
// Queue for review instead of auto-opening investigations — reachability/severity are filters, not routers.
// The only surviving auto-route is trufflehog's verified (live-checked) secret → finding.
func TestQueueFirstDispositions(t *testing.T) {
	r := BuiltIns()
	for _, id := range []string{"grype", "osv-scanner", "opengrep", "semgrep", "govulncheck", "route-map"} {
		c, ok := r.Get(id)
		if !ok {
			t.Fatalf("capability %q not registered", id)
		}
		if d := c.Manifest().Dispositions; len(d) != 0 {
			t.Errorf("%s should declare no dispositions (queue-first), got %v", id, d)
		}
	}

	th, ok := r.Get("trufflehog")
	if !ok {
		t.Fatal("trufflehog not registered")
	}
	rules := th.Manifest().Dispositions
	// A verified secret is confirmed → finding; an unverified one has no auto-route → stays in the Queue.
	if got := disposition.Evaluate(obs("medium", map[string]string{"verified": "true"}), rules); got != disposition.ActionFinding {
		t.Errorf("verified secret: got %q, want finding", got)
	}
	if got := disposition.Evaluate(obs("medium", map[string]string{"verified": "false"}), rules); got != disposition.ActionReview {
		t.Errorf("unverified secret: got %q, want review (queue)", got)
	}
}
