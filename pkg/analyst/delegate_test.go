package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/llm"
)

func hasTool(p Profile, name string) bool {
	for _, tl := range p.ToolSet() {
		if tl.Name == name {
			return true
		}
	}
	return false
}

func TestDelegateRunsSpecialist(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	// The specialist reads then answers.
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"list_findings","args":{}}`,
		`{"answer":"There are no findings yet."}`,
	}}
	svc := NewService(db, nil, nil, "", mock)

	res, err := svc.Delegate(ctx, "", "report-writer", "summarize the findings", profileToolNames(ProfileByID("report-writer")))
	if err != nil {
		t.Fatal(err)
	}
	if res.Profile != "report-writer" || !strings.Contains(res.Answer, "no findings") {
		t.Fatalf("delegation result = %+v", res)
	}
	if !containsStr(res.ToolsUsed, "list_findings") {
		t.Fatalf("tools used = %v", res.ToolsUsed)
	}
}

func TestDelegateToolViaExecuteFor(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"list_findings","args":{}}`,
		`{"answer":"done"}`,
	}}
	svc := NewService(db, nil, nil, "", mock)

	out, err := svc.executeFor("", nil)(ctx, agent.ToolCall{Tool: "delegate", Args: map[string]any{"agent": "report-writer", "task": "summarize"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "report-writer") || !strings.Contains(out, "done") {
		t.Fatalf("delegate tool output = %s", out)
	}
}

func TestDelegateRefusesNonSpecialistTarget(t *testing.T) {
	ctx := context.Background()
	svc := NewService(migratedStore(t), nil, nil, "", &llm.MockProvider{})

	for _, target := range []string{"lead", "generalist"} {
		if _, err := svc.executeFor("", nil)(ctx, agent.ToolCall{Tool: "delegate", Args: map[string]any{"agent": target, "task": "x"}}); err == nil || !strings.Contains(err.Error(), "specialist") {
			t.Fatalf("delegating to %q should be refused, got %v", target, err)
		}
	}
}

func TestDelegationCapabilityByProfile(t *testing.T) {
	// The Lead delegates; the pentester can decompose a large engagement deeper (ADR-0047).
	for _, id := range []string{"lead", "pentester"} {
		if !hasTool(ProfileByID(id), "delegate") {
			t.Fatalf("%q should have the delegate tool", id)
		}
	}
	// Narrow specialists must NOT delegate — deeper delegation stays with coordinator roles.
	for _, id := range []string{"code-analysis", "vuln-validator", "report-writer", "knowledge-scribe"} {
		if hasTool(ProfileByID(id), "delegate") {
			t.Fatalf("specialist %q must not have delegate", id)
		}
	}
}

// Deeper delegation is bounded: a sub-agent already at the max nesting depth is refused a further delegate,
// while one below the cap proceeds (ADR-0047).
func TestDeeperDelegationBoundedByDepth(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	mock := &llm.MockProvider{Responses: []string{`{"answer":"done"}`}}
	svc := NewService(db, nil, nil, "", mock)
	exec := svc.executeFor("", nil)
	call := agent.ToolCall{Tool: "delegate", Args: map[string]any{"agent": "report-writer", "task": "summarize"}}

	atCap := withDelegationDepth(ctx, maxDelegationDepth())
	if _, err := exec(atCap, call); err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("delegate at max depth should be refused, got %v", err)
	}
	belowCap := withDelegationDepth(ctx, maxDelegationDepth()-1)
	if _, err := exec(belowCap, call); err != nil {
		t.Fatalf("delegate below max depth should proceed, got %v", err)
	}
}

// Delegate runs its sub-agent one level deeper, so depth accumulates down a chain (ADR-0047).
func TestDelegateIncrementsDepth(t *testing.T) {
	ctx := context.Background()
	if delegationDepth(ctx) != 0 {
		t.Fatal("a fresh context should be depth 0")
	}
	d1 := withDelegationDepth(ctx, delegationDepth(ctx)+1)
	d2 := withDelegationDepth(d1, delegationDepth(d1)+1)
	if delegationDepth(d1) != 1 || delegationDepth(d2) != 2 {
		t.Fatalf("depth did not accumulate: d1=%d d2=%d", delegationDepth(d1), delegationDepth(d2))
	}
}

func TestDelegateIsGated(t *testing.T) {
	if !DefaultPolicy().NeedsApproval("delegate", "lead") {
		t.Fatal("delegate must be gated by default")
	}
}
