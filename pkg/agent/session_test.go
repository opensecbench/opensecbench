package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestSessionPausesOnGatedToolThenResumes(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"run_scan","args":{"target":"api"}}`, // gated -> pause
		`{"answer":"The scan found 2 issues; both look real."}`,
	}}

	executed := 0
	s := &Session{
		Provider: mock,
		Tools:    []Tool{{Name: "run_scan", Description: "run a scan"}},
		Gate:     func(c ToolCall) bool { return c.Tool == "run_scan" }, // gated
		Execute: func(_ context.Context, _ ToolCall) (string, error) {
			executed++
			return "2 issues", nil
		},
	}

	ctx := context.Background()
	out, err := s.Advance(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: s.SystemPrompt()},
		{Role: llm.RoleUser, Content: "scan the api"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Done || out.Pending == nil || out.Pending.Tool != "run_scan" {
		t.Fatalf("expected pause on run_scan, got %+v", out)
	}
	if executed != 0 {
		t.Fatal("gated tool executed before approval")
	}

	// Approve and resume.
	out, err = s.Resume(ctx, out.Messages, *out.Pending, true)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done || !strings.Contains(out.Answer, "2 issues") {
		t.Fatalf("resume did not complete: %+v", out)
	}
	if executed != 1 {
		t.Fatalf("expected the tool to run once on approval, ran %d times", executed)
	}
}

func TestSessionResumeDenied(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"run_scan","args":{}}`,
		`{"answer":"Understood, not running it."}`,
	}}
	executed := false
	s := &Session{
		Provider: mock,
		Gate:     func(ToolCall) bool { return true },
		Execute:  func(context.Context, ToolCall) (string, error) { executed = true; return "", nil },
	}
	ctx := context.Background()
	out, _ := s.Advance(ctx, []llm.Message{{Role: llm.RoleUser, Content: "scan"}})
	if out.Pending == nil {
		t.Fatal("expected pending")
	}
	out, err := s.Resume(ctx, out.Messages, *out.Pending, false)
	if err != nil {
		t.Fatal(err)
	}
	if executed {
		t.Fatal("denied tool must not run")
	}
	if !out.Done {
		t.Fatal("expected completion after denial")
	}
}

func TestSessionAutoRunsUngatedTool(t *testing.T) {
	mock := &llm.MockProvider{Responses: []string{
		`{"tool":"list_projects","args":{}}`,
		`{"answer":"one project"}`,
	}}
	s := &Session{
		Provider: mock,
		Gate:     func(ToolCall) bool { return false }, // auto-approve everything
		Execute:  func(context.Context, ToolCall) (string, error) { return "[]", nil },
	}
	out, err := s.Advance(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "list"}})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Done || out.Pending != nil {
		t.Fatalf("ungated tool should not pause: %+v", out)
	}
}

type budgetProvider struct{}

func (budgetProvider) Name() string { return "budget" }
func (budgetProvider) Complete(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	return llm.CompletionResponse{Text: `{"tool":"noop","args":{}}`, OutputTokens: 100}, nil
}

func TestSessionStopsAtTokenBudget(t *testing.T) {
	s := &Session{
		Provider:    budgetProvider{},
		Gate:        func(ToolCall) bool { return false },
		Execute:     func(context.Context, ToolCall) (string, error) { return "ok", nil },
		TokenBudget: 50,
	}
	out, err := s.Advance(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Answer, "budget") {
		t.Fatalf("expected budget stop, got %q", out.Answer)
	}
}
