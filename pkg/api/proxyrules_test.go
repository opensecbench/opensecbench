package api

import (
	"regexp"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestRuleEngineApply(t *testing.T) {
	e := &ruleEngine{}
	e.set([]compiledRule{
		{target: model.RuleTargetRequestBody, re: regexp.MustCompile("SECRET"), replace: "X"},
		{target: model.RuleTargetResponseBody, re: regexp.MustCompile(`(?i)admin`), replace: "user"},
	})

	if !e.NeedsResponseBody() {
		t.Fatal("a response_body rule should require buffering")
	}
	if _, _, _, b := e.ProcessRequest("GET", "http://x", "", "token=SECRET"); b != "token=X" {
		t.Fatalf("request body = %q, want token=X", b)
	}
	if _, _, rb := e.ProcessResponse(200, "", "Admin panel"); rb != "user panel" {
		t.Fatalf("response body = %q, want 'user panel'", rb)
	}

	// With only a request-side rule, the proxy need not buffer responses.
	e.set([]compiledRule{{target: model.RuleTargetURL, re: regexp.MustCompile("a"), replace: "b"}})
	if e.NeedsResponseBody() {
		t.Fatal("a url rule should not require response buffering")
	}
}
