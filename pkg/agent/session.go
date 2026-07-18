package agent

import (
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

// Session is a resumable agent run. Unlike Loop (which runs to completion), a Session advances
// until it produces a final answer or reaches a gated tool call, at which point it pauses so a
// human can approve or deny. Its entire state is the message history, so it persists to a thread
// and resumes later (ADR-0006). This is the basis for the async approval queue.
type Session struct {
	Provider llm.Provider
	Tools    []Tool
	// Gate reports whether a tool call needs human approval (true) or may run automatically.
	Gate func(call ToolCall) bool
	// Execute runs an (approved or auto) tool call.
	Execute  func(ctx context.Context, call ToolCall) (string, error)
	Audit    func(action, detail string)
	MaxSteps int
	Model    string
	// MaxTokens caps output tokens per completion (passed to the provider).
	MaxTokens int
	// TokenBudget caps cumulative tokens for this advance; 0 means unlimited.
	TokenBudget int
}

// Outcome is the result of an Advance/Resume. Exactly one of Done/Pending is meaningful.
type Outcome struct {
	Done         bool          // final answer produced
	Answer       string        // set when Done
	Pending      *ToolCall     // set when paused awaiting approval
	Messages     []llm.Message // full conversation (persist this)
	InputTokens  int
	OutputTokens int
}

// SystemPrompt is the system message for this session's tools.
func (s *Session) SystemPrompt() string { return buildSystemPrompt(s.Tools) }

// Advance runs the model from the given message history until a final answer or a gated tool call.
// Auto-approved tools execute inline; a gated tool call pauses the run.
func (s *Session) Advance(ctx context.Context, messages []llm.Message) (Outcome, error) {
	maxSteps := s.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	out := Outcome{Messages: messages}

	for step := 0; step < maxSteps; step++ {
		resp, err := s.Provider.Complete(ctx, llm.CompletionRequest{Messages: out.Messages, Model: s.Model, MaxTokens: s.MaxTokens})
		if err != nil {
			return out, err
		}
		out.InputTokens += resp.InputTokens
		out.OutputTokens += resp.OutputTokens
		out.Messages = append(out.Messages, llm.Message{Role: llm.RoleAssistant, Content: resp.Text})

		if s.TokenBudget > 0 && out.InputTokens+out.OutputTokens >= s.TokenBudget {
			s.audit("agent.budget.exceeded", "")
			out.Done = true
			out.Answer = "(stopped: token budget exceeded)"
			return out, nil
		}

		rep, ok := parseReply(resp.Text)
		if !ok || rep.Tool == "" {
			out.Done = true
			out.Answer = rep.Answer
			if out.Answer == "" {
				out.Answer = resp.Text
			}
			return out, nil
		}

		call := ToolCall{Tool: rep.Tool, Args: rep.Args}
		s.audit("agent.tool.proposed", call.Tool)

		if s.Gate != nil && s.Gate(call) {
			c := call
			out.Pending = &c
			return out, nil
		}

		out.Messages = s.runTool(ctx, out.Messages, call)
	}

	out.Done = true
	out.Answer = "(stopped: reached the step limit without a final answer)"
	return out, nil
}

// Resume continues a paused run after a human decision on the pending tool call.
func (s *Session) Resume(ctx context.Context, messages []llm.Message, call ToolCall, approved bool) (Outcome, error) {
	if !approved {
		s.audit("agent.tool.denied", call.Tool)
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q was denied by the human. Do not retry it; continue or give your final answer.", call.Tool)})
		return s.Advance(ctx, messages)
	}
	messages = s.runTool(ctx, messages, call)
	return s.Advance(ctx, messages)
}

// runTool executes a call and appends its result (or error) to the conversation.
func (s *Session) runTool(ctx context.Context, messages []llm.Message, call ToolCall) []llm.Message {
	out, err := s.Execute(ctx, call)
	if err != nil {
		s.audit("agent.tool.error", call.Tool)
		return append(messages, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q errored: %s", call.Tool, err.Error())})
	}
	s.audit("agent.tool.executed", call.Tool)
	return append(messages, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q result:\n%s", call.Tool, out)})
}

func (s *Session) audit(action, detail string) {
	if s.Audit != nil {
		s.Audit(action, detail)
	}
}
