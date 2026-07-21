package api

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func toolVuln(rule, sev string, attrs map[string]string) model.Observation {
	return model.Observation{Origin: model.OriginTool, RuleID: rule, Severity: sev, Attributes: attrs}
}

func TestLooksLikeVuln(t *testing.T) {
	cases := []struct {
		o    model.Observation
		want bool
	}{
		{toolVuln("CVE-2024-1234", "high", nil), true},
		{toolVuln("GHSA-xxxx-yyyy", "medium", nil), true},
		{toolVuln("GO-2022-0969", "high", nil), true},
		{toolVuln("python.sql-injection", "high", nil), false},                       // SAST rule, not a vuln id
		{model.Observation{Origin: model.OriginThread, RuleID: "CVE-2024-1"}, false}, // not a tool observation
	}
	for _, c := range cases {
		if got := looksLikeVuln(c.o); got != c.want {
			t.Errorf("looksLikeVuln(%q, origin=%q) = %v, want %v", c.o.RuleID, c.o.Origin, got, c.want)
		}
	}
}

func TestMentionsPackage(t *testing.T) {
	o := model.Observation{Title: "Denial of service", Detail: "vulnerability in flask 2.0.1 affects the app"}
	if !mentionsPackage(o, "flask") {
		t.Error("should match 'flask' as a token in the detail")
	}
	// Token boundary: 'flask' must not match inside 'flask-login'.
	o2 := model.Observation{Detail: "issue in flask-login 0.6"}
	if mentionsPackage(o2, "flask") {
		t.Error("'flask' should not match the substring inside 'flask-login'")
	}
	// The longer, correct name does match.
	if !mentionsPackage(o2, "flask-login") {
		t.Error("'flask-login' should match")
	}
	// Short names are ignored to avoid noise.
	if mentionsPackage(model.Observation{Detail: "go go go"}, "go") {
		t.Error("names shorter than 3 chars must not match")
	}
}

func TestSummarizeVulns(t *testing.T) {
	obs := []model.Observation{
		toolVuln("CVE-1", "medium", nil),
		toolVuln("CVE-2", "critical", map[string]string{"reachable": "true"}),
		toolVuln("CVE-3", "low", nil),
	}
	worst, reachable := summarizeVulns(obs)
	if worst != "critical" {
		t.Errorf("worst = %q, want critical", worst)
	}
	if !reachable {
		t.Error("should be reachable (one obs has reachable=true)")
	}

	worst2, reachable2 := summarizeVulns([]model.Observation{toolVuln("CVE-9", "high", nil)})
	if worst2 != "high" || reachable2 {
		t.Errorf("got (%q, %v), want (high, false)", worst2, reachable2)
	}
}
