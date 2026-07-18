package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

// AnthropicProvider talks to the Anthropic Messages API natively (system prompt is a top-level
// field; only user/assistant turns go in messages).
type AnthropicProvider struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// Name identifies the provider.
func (a *AnthropicProvider) Name() string { return "anthropic" }

// Complete calls the Messages API.
func (a *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if a.APIKey == "" {
		return CompletionResponse{}, errors.New("llm anthropic: API key not set")
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	model := req.Model
	if model == "" {
		model = a.Model
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	var system string
	msgs := make([]Message, 0, len(req.Messages))
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			system += m.Content + "\n"
			continue
		}
		msgs = append(msgs, m)
	}

	payload := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
		"messages":   msgs,
	}
	if system != "" {
		payload["system"] = system
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	client := a.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm anthropic: %s: %s", resp.Status, string(b))
	}

	var out struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CompletionResponse{}, err
	}
	var text string
	for _, c := range out.Content {
		if c.Type == "text" {
			text += c.Text
		}
	}
	return CompletionResponse{Text: text, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}, nil
}
