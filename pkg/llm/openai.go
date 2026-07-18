package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// OpenAIProvider talks to any OpenAI-compatible chat/completions API. This covers OpenAI, Azure
// OpenAI, DeepSeek, xAI Grok, and a local Ollama server (`/v1`), which differ only by base URL,
// model, and key.
type OpenAIProvider struct {
	Label   string
	BaseURL string // e.g. https://api.deepseek.com/v1 or http://127.0.0.1:11434/v1
	APIKey  string // empty for keyless local servers
	Model   string
	HTTP    *http.Client
}

// Name identifies the provider.
func (p *OpenAIProvider) Name() string {
	if p.Label != "" {
		return p.Label
	}
	return "openai-compat"
}

// Complete calls the chat/completions endpoint.
func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if p.BaseURL == "" {
		return CompletionResponse{}, errors.New("llm openai: base URL not set")
	}
	model := req.Model
	if model == "" {
		model = p.Model
	}

	payload := map[string]any{
		"model":    model,
		"messages": req.Messages,
	}
	if req.MaxTokens > 0 {
		payload["max_tokens"] = req.MaxTokens
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	endpoint := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	client := p.HTTP
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
		return CompletionResponse{}, fmt.Errorf("llm %s: %s: %s", p.Name(), resp.Status, string(b))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CompletionResponse{}, err
	}
	if len(out.Choices) == 0 {
		return CompletionResponse{}, errors.New("llm openai: no choices in response")
	}
	return CompletionResponse{
		Text:         out.Choices[0].Message.Content,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}
