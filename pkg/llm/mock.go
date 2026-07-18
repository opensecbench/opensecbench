package llm

import "context"

// MockProvider returns scripted responses in order. It is used to test the agent loop
// deterministically without a real backend.
type MockProvider struct {
	Responses []string
	calls     int
}

// Name identifies the provider.
func (m *MockProvider) Name() string { return "mock" }

// Calls reports how many completions have been requested.
func (m *MockProvider) Calls() int { return m.calls }

// Complete returns the next scripted response.
func (m *MockProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	var text string
	if m.calls < len(m.Responses) {
		text = m.Responses[m.calls]
	} else {
		text = `{"answer":"done"}`
	}
	m.calls++
	return CompletionResponse{Text: text, OutputTokens: len(text) / 4}, nil
}
