package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestLoopExecutesToolThenAnswers(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		"```json\n{\"tool\":\"list_projects\",\"args\":{}}\n```", // tolerate code fences + prose
		`{"answer":"There is 1 project: Acme."}`,
	}}

	var executed []string
	var audited []string
	loop := &Loop{
		Provider: mock,
		Tools:    []Tool{{Name: "list_projects", Description: "list projects"}},
		Approve:  func(_ context.Context, _ ToolCall) (bool, error) { return true, nil },
		Execute: func(_ context.Context, c ToolCall) (string, error) {
			executed = append(executed, c.Tool)
			return "Acme", nil
		},
		Audit: func(action, _ string) { audited = append(audited, action) },
	}

	res, err := loop.Run(context.Background(), "how many projects?")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Answer, "Acme") {
		t.Fatalf("answer = %q", res.Answer)
	}
	if len(executed) != 1 || executed[0] != "list_projects" {
		t.Fatalf("executed = %v", executed)
	}
	if len(res.Steps) != 1 || !res.Steps[0].Approved || res.Steps[0].Result != "Acme" {
		t.Fatalf("steps = %+v", res.Steps)
	}
	if !contains(audited, "agent.tool.proposed") || !contains(audited, "agent.tool.executed") {
		t.Fatalf("audit trail missing events: %v", audited)
	}
}

func TestLoopRespectsDenial(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"run_nuclei","args":{"target":"api.acme.com"}}`,
		`{"answer":"Understood, I will not run it."}`,
	}}

	executed := false
	loop := &Loop{
		Provider: mock,
		Approve:  func(_ context.Context, _ ToolCall) (bool, error) { return false, nil },
		Execute: func(_ context.Context, _ ToolCall) (string, error) {
			executed = true
			return "", nil
		},
	}
	res, err := loop.Run(context.Background(), "scan the API")
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("denied tool must not be executed")
	}
	if len(res.Steps) != 1 || res.Steps[0].Approved {
		t.Fatalf("expected one denied step, got %+v", res.Steps)
	}
}

func TestLoopStopsAtStepCap(t *testing.T) {
	loop := &Loop{
		Provider: alwaysToolProvider{},
		MaxSteps: 3,
		Approve:  func(_ context.Context, _ ToolCall) (bool, error) { return true, nil },
		Execute:  func(_ context.Context, _ ToolCall) (string, error) { return "ok", nil },
	}
	res, err := loop.Run(context.Background(), "loop forever")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 3 {
		t.Fatalf("expected 3 steps at the cap, got %d", len(res.Steps))
	}
	if !strings.Contains(res.Answer, "step limit") {
		t.Fatalf("answer = %q", res.Answer)
	}
}

type alwaysToolProvider struct{}

func (alwaysToolProvider) Name() string { return "always-tool" }
func (alwaysToolProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	return llm.CompletionResponse{Text: `{"tool":"noop","args":{}}`}, nil
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
