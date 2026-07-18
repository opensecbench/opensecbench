// Package llm is the provider abstraction for the agent runtime (ADR-0006). A provider is
// inference-only: it turns a list of messages into a text completion. The tool-calling loop lives
// in pkg/agent, so every backend — API, local, or a CLI binary — works the same way.
package llm

import "context"

// Message roles. RoleTool is a tool-result turn (ADR-0017): its Content is the tool's output (or an
// error, when ToolError), and ToolCallID links it to the assistant ToolCall it answers.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Message is one turn in a conversation. The canonical tool turns (ADR-0017) are: an assistant turn
// carrying ToolCalls, followed by a RoleTool turn carrying the result (ToolCallID + Content, ToolError
// on failure). This form is vendor-portable — each provider adapter renders it to its own wire format
// (native tool blocks, or the prompted text protocol).
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`   // assistant turn: structured calls
	ToolCallID string     `json:"tool_call_id,omitempty"` // tool turn: the call this result answers
	ToolError  bool       `json:"tool_error,omitempty"`   // tool turn: the call failed / was denied
}

// CompletionRequest asks a provider for the next assistant message. Tools, when set, are the tools the
// model may call; a tool-aware provider renders them natively, the prompted adapter into the prompt.
type CompletionRequest struct {
	Messages  []Message
	Model     string
	MaxTokens int
	Tools     []ToolDef
}

// CompletionResponse is a provider's reply plus token usage. Exactly one of Text (a final answer) or
// ToolCalls (requested tool invocations) is meaningful.
type CompletionResponse struct {
	Text         string
	ToolCalls    []ToolCall
	InputTokens  int
	OutputTokens int
}

// Provider produces completions from some LLM backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
