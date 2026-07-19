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

func TestOnlyLeadCanDelegate(t *testing.T) {
	if !hasTool(ProfileByID("lead"), "delegate") {
		t.Fatal("the Lead must have the delegate tool")
	}
	// Specialists must NOT have delegate — delegation nests one level only.
	for _, id := range []string{"code-analysis", "vuln-validator", "pentester", "report-writer", "generalist"} {
		if id != "generalist" && hasTool(ProfileByID(id), "delegate") {
			t.Fatalf("specialist %q must not have delegate", id)
		}
	}
}

func TestDelegateIsGated(t *testing.T) {
	if !DefaultPolicy().NeedsApproval("delegate", "lead") {
		t.Fatal("delegate must be gated by default")
	}
}
