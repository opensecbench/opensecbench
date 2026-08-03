package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

// approvedKey marks a context as carrying an explicit human approval for the tool call about to run.
type approvedKey struct{}

// WithApproved tags ctx as the execution of a tool call a human just explicitly approved. It scopes to
// exactly that call — not the auto-run steps that follow it in the same Advance.
func WithApproved(ctx context.Context) context.Context {
	return context.WithValue(ctx, approvedKey{}, true)
}

// Approved reports whether the running tool call was explicitly approved by a human (as opposed to
// auto-approved by policy or run on an unattended path). Executors use it to decide how far a single
// approval propagates — e.g. what a delegated sub-agent may run without further prompting.
func Approved(ctx context.Context) bool { v, _ := ctx.Value(approvedKey{}).(bool); return v }

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
	// OnMessage, if set, is called with each message (assistant turn, tool result, stop notice) the moment
	// it is produced during a turn — so a caller can stream the run's steps live instead of only seeing the
	// batch at the end. Fired in-loop, so keep it non-blocking.
	OnMessage func(m llm.Message)
	// OnDelta, if set, receives assistant text token-by-token as it is generated (real streaming), before
	// the completed OnMessage for that turn. Providers that can't stream deliver the whole text as one delta.
	OnDelta func(text string)
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

// SystemPrompt is the system message for this session (persona only; the tool catalog is added per
// provider at completion time — natively or by the prompted adapter, ADR-0017).
func (s *Session) SystemPrompt() string { return buildSystemPrompt() }

// Advance runs the model from the given message history until a final answer or a gated tool call.
// Auto-approved tools execute inline; a gated tool call pauses the run.
func (s *Session) Advance(ctx context.Context, messages []llm.Message) (Outcome, error) {
	provider := llm.EnsureToolAware(s.Provider)
	maxSteps := s.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	out := Outcome{Messages: messages}

	for step := 0; step < maxSteps; step++ {
		// Stream text deltas to OnDelta as they generate (real token streaming where the provider supports
		// it; a single whole-text delta otherwise). The full response still drives the tool loop below.
		var onDelta llm.StreamHandler
		if s.OnDelta != nil {
			onDelta = func(d string) { s.OnDelta(d) }
		}
		resp, err := llm.Stream(ctx, provider, llm.CompletionRequest{Messages: out.Messages, Model: s.Model, MaxTokens: s.MaxTokens, Tools: s.Tools}, onDelta)
		if err != nil {
			return out, err
		}
		out.InputTokens += resp.InputTokens
		out.OutputTokens += resp.OutputTokens
		am := llm.Message{Role: llm.RoleAssistant, Content: resp.Text, ToolCalls: resp.ToolCalls}
		out.Messages = append(out.Messages, am)
		s.emit(am)

		if s.TokenBudget > 0 && out.InputTokens+out.OutputTokens >= s.TokenBudget {
			s.audit("agent.budget.exceeded", "")
			out.Done = true
			out.Answer = "(stopped: token budget exceeded)"
			return out, nil
		}

		if len(resp.ToolCalls) == 0 {
			out.Done = true
			out.Answer = strings.TrimSpace(resp.Text)
			return out, nil
		}

		call := resp.ToolCalls[0]
		s.audit("agent.tool.proposed", call.Tool)

		// Validate arguments against the tool schema before gating or executing (ADR-0017).
		if verr := validateCall(s.Tools, call); verr != nil {
			s.audit("agent.tool.invalid", call.Tool)
			inv := toolResult(call, fmt.Sprintf("Tool %q arguments were invalid: %s. Fix the arguments and call it again, or give your final answer.", call.Tool, verr.Error()), true)
			out.Messages = append(out.Messages, inv)
			s.emit(inv)
			continue
		}

		if s.Gate != nil && s.Gate(call) {
			c := call
			out.Pending = &c
			return out, nil
		}

		out.Messages = s.runTool(ctx, out.Messages, call)
	}

	out.Done = true
	out.Answer = "(Stopped: I reached my step limit for this turn before finishing. Ask me to continue and I'll pick up where I left off.)"
	// Persist the notice as a real assistant turn. Otherwise the last message is a tool result and the UI
	// shows nothing — a completed-but-capped run looks identical to a hang.
	stop := llm.Message{Role: llm.RoleAssistant, Content: out.Answer}
	out.Messages = append(out.Messages, stop)
	s.emit(stop)
	return out, nil
}

// Resume continues a paused run after a human decision on the pending tool call.
func (s *Session) Resume(ctx context.Context, messages []llm.Message, call ToolCall, approved bool) (Outcome, error) {
	if !approved {
		s.audit("agent.tool.denied", call.Tool)
		den := toolResult(call, fmt.Sprintf("Tool %q was denied by the human. Do not retry it; continue or give your final answer.", call.Tool), true)
		s.emit(den)
		messages = append(messages, den)
		return s.Advance(ctx, messages)
	}
	// Only this call carries the human's approval; the auto-run steps that follow in Advance do not.
	messages = s.runTool(WithApproved(ctx), messages, call)
	return s.Advance(ctx, messages)
}

// runTool executes a call and appends its canonical result (or error) turn to the conversation.
func (s *Session) runTool(ctx context.Context, messages []llm.Message, call ToolCall) []llm.Message {
	out, err := s.Execute(ctx, call)
	var res llm.Message
	if err != nil {
		s.audit("agent.tool.error", call.Tool)
		res = toolResult(call, fmt.Sprintf("Tool %q errored: %s", call.Tool, err.Error()), true)
	} else {
		s.audit("agent.tool.executed", call.Tool)
		res = toolResult(call, out, false)
	}
	s.emit(res)
	return append(messages, res)
}

// emit streams one message to the live listener (OnMessage), if set.
func (s *Session) emit(m llm.Message) {
	if s.OnMessage != nil {
		s.OnMessage(m)
	}
}

// toolResult builds the canonical RoleTool turn answering a call (ADR-0017): Content is the tool's
// output, or a complete error/denial instruction when isErr; ToolCallID links it to the call.
func toolResult(call ToolCall, content string, isErr bool) llm.Message {
	return llm.Message{Role: llm.RoleTool, Content: content, ToolCallID: call.ID, ToolError: isErr}
}

func (s *Session) audit(action, detail string) {
	if s.Audit != nil {
		s.Audit(action, detail)
	}
}
