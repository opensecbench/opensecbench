package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestPolicyBaseIsConservative(t *testing.T) {
	p := DefaultPolicy()
	// Sensitive tools ask for approval by default...
	for _, tool := range []string{"send_request", "run_code", "run_capability", "create_finding"} {
		if !p.NeedsApproval(tool, "pentester") {
			t.Errorf("%s should require approval by default", tool)
		}
	}
	// ...reads run automatically.
	for _, tool := range []string{"read_file", "get_finding", "list_context", "read_context"} {
		if p.NeedsApproval(tool, "generalist") {
			t.Errorf("%s should be auto by default", tool)
		}
	}
}

func TestPolicyToolOverride(t *testing.T) {
	// Trust earned: promote send_request to auto for everyone; run_code still asks.
	p := NewPolicy([]Rule{{Tool: "send_request", Decision: DecisionAuto}})
	if p.NeedsApproval("send_request", "pentester") {
		t.Fatal("send_request should now be auto")
	}
	if !p.NeedsApproval("run_code", "pentester") {
		t.Fatal("run_code should still require approval")
	}
}

func TestPolicyProfileSpecificBeatsToolOnly(t *testing.T) {
	p := NewPolicy([]Rule{
		{Tool: "run_capability", Decision: DecisionAuto},                          // auto for all profiles...
		{Tool: "run_capability", Profile: "pentester", Decision: DecisionApprove}, // ...except the pentester
	})
	if p.NeedsApproval("run_capability", "code-analysis") {
		t.Fatal("code-analysis run_capability should be auto (tool-only rule)")
	}
	if !p.NeedsApproval("run_capability", "pentester") {
		t.Fatal("pentester run_capability should still approve (profile-specific rule wins)")
	}
}

func TestPolicyCanTightenAReadToApprove(t *testing.T) {
	// The policy tightens as well as relaxes: require approval before reading ingested correspondence.
	p := NewPolicy([]Rule{{Tool: "read_context", Decision: DecisionApprove}})
	if !p.NeedsApproval("read_context", "generalist") {
		t.Fatal("read_context should require approval under this rule")
	}
}

func TestServicePolicyFromSettingsDrivesTheGate(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	svc := NewService(db, nil, nil, "", &llm.MockProvider{})

	// Default: send_request pauses for approval.
	if !svc.loadPolicy(ctx).NeedsApproval("send_request", "pentester") {
		t.Fatal("default policy should gate send_request")
	}

	// Operator promotes send_request to auto.
	if err := db.SetSetting(ctx, ApprovalPolicySetting, `[{"tool":"send_request","decision":"auto"}]`); err != nil {
		t.Fatal(err)
	}
	policy := svc.loadPolicy(ctx)
	sess := svc.session("proj", ProfileByID("pentester"), policy, svc.provider, "")
	if sess.Gate(agent.ToolCall{Tool: "send_request"}) {
		t.Fatal("send_request should no longer be gated after the rule")
	}
	if !sess.Gate(agent.ToolCall{Tool: "run_code"}) {
		t.Fatal("run_code should still be gated")
	}
}
