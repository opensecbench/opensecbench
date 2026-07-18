// Package llm is the provider abstraction for the agent runtime (ADR-0006). A provider is
// inference-only: it turns a list of messages into a text completion. The tool-calling loop lives
// in pkg/agent, so every backend — API, local, or a CLI binary — works the same way.
package llm

import "context"

// Message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

// Message is one turn in a conversation.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest asks a provider for the next assistant message.
type CompletionRequest struct {
	Messages  []Message
	Model     string
	MaxTokens int
}

// CompletionResponse is a provider's reply plus token usage.
type CompletionResponse struct {
	Text         string
	InputTokens  int
	OutputTokens int
}

// Provider produces completions from some LLM backend.
type Provider interface {
	Name() string
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
