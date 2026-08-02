package analyst

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestPolicyBaseIsConsequenceTier(t *testing.T) {
	p := DefaultPolicy() // Cautious envelope
	// External + Execute actions confirm by default — you can't un-send a request or un-run code.
	for _, tool := range []string{"send_request", "web_fetch", "run_code", "run_capability", "run_playbook", "delegate"} {
		if !p.NeedsApproval(tool, "pentester") {
			t.Errorf("%s (external/execute) should require approval by default", tool)
		}
	}
	// Reversible writes run freely — capability parity with the human, oversight is undo/audit (ADR-0053/0054).
	for _, tool := range []string{"create_finding", "set_coverage", "set_finding_status", "triage_observation", "add_kb_entry", "workspace_write", "show"} {
		if p.NeedsApproval(tool, "pentester") {
			t.Errorf("%s (reversible) should run without approval by default", tool)
		}
	}
	// ...as do reads.
	for _, tool := range []string{"read_file", "get_finding", "list_context", "read_context"} {
		if p.NeedsApproval(tool, "generalist") {
			t.Errorf("%s should be auto by default", tool)
		}
	}
}

// The autonomy envelope (control surface) shifts the confirm line without touching the capability set.
func TestPolicyAutonomyEnvelope(t *testing.T) {
	trusted := DefaultPolicy().WithAutonomy(AutonomyTrusted)
	// Trusted lets external/execute run free...
	for _, tool := range []string{"send_request", "run_code", "delegate"} {
		if trusted.NeedsApproval(tool, "pentester") {
			t.Errorf("%s should run free under the Trusted envelope", tool)
		}
	}
	// ...but a per-tool override still wins (fine-grained trust curve on top).
	pinned := NewPolicy([]Rule{{Tool: "run_code", Decision: DecisionApprove}}).WithAutonomy(AutonomyTrusted)
	if !pinned.NeedsApproval("run_code", "pentester") {
		t.Error("an explicit approve rule must override the Trusted envelope for run_code")
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
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	// Default: send_request pauses for approval.
	if !svc.loadPolicy(ctx).NeedsApproval("send_request", "pentester") {
		t.Fatal("default policy should gate send_request")
	}

	// Operator promotes send_request to auto.
	if err := db.SetSetting(ctx, ApprovalPolicySetting, `[{"tool":"send_request","decision":"auto"}]`); err != nil {
		t.Fatal(err)
	}
	policy := svc.loadPolicy(ctx)
	sess := svc.session("proj", "thread", ProfileByID("pentester"), policy, svc.provider, "", "")
	if sess.Gate(agent.ToolCall{Tool: "send_request"}) {
		t.Fatal("send_request should no longer be gated after the rule")
	}
	if !sess.Gate(agent.ToolCall{Tool: "run_code"}) {
		t.Fatal("run_code should still be gated")
	}
}

// The autonomy setting (the header selector's control surface) flips the gate end to end.
func TestServiceAutonomyFromSettingsDrivesTheGate(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	svc := NewService(store.NewCombinedManager(db), nil, nil, "", &llm.MockProvider{})

	// Default (cautious): send_request and run_code both pause.
	if !svc.loadPolicy(ctx).NeedsApproval("send_request", "pentester") || !svc.loadPolicy(ctx).NeedsApproval("run_code", "pentester") {
		t.Fatal("cautious should gate external + execute")
	}

	// Human raises the envelope to trusted.
	if err := db.SetSetting(ctx, AutonomySetting, string(AutonomyTrusted)); err != nil {
		t.Fatal(err)
	}
	p := svc.loadPolicy(ctx)
	if p.NeedsApproval("send_request", "pentester") || p.NeedsApproval("run_code", "pentester") {
		t.Fatal("trusted should let external + execute run free")
	}
	// Reversible always ran free regardless.
	if p.NeedsApproval("set_finding_status", "pentester") {
		t.Fatal("reversible should never gate")
	}
}
