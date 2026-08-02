package llm

import (
	"context"
	"sync"
)

// MockProvider returns scripted responses in order. It is used to test the agent loop
// deterministically without a real backend. Safe for concurrent use — the plan runner
// delegates to it from parallel wave goroutines.
type MockProvider struct {
	Responses []string
	mu        sync.Mutex
	calls     int
}

// Name identifies the provider.
func (m *MockProvider) Name() string { return "mock" }

// Calls reports how many completions have been requested.
func (m *MockProvider) Calls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// Complete returns the next scripted response.
func (m *MockProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	m.mu.Lock()
	i := m.calls
	m.calls++
	m.mu.Unlock()

	text := `{"answer":"done"}`
	if i < len(m.Responses) {
		text = m.Responses[i]
	}
	return CompletionResponse{Text: text, OutputTokens: len(text) / 4}, nil
}
