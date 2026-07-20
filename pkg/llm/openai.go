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
	// AuthHeader overrides how the key is sent. Empty (default) uses "Authorization: Bearer <key>";
	// Azure AI Foundry uses "api-key: <key>" (ADR-0052).
	AuthHeader string
	// UseNativeTools sends tools and tool turns as native tool_calls / role:"tool" messages
	// (ADR-0017) instead of the prompted text protocol. The config paths enable it by default
	// (OSB_LLM_NATIVE_TOOLS=0 forces the prompted fallback).
	UseNativeTools bool
}

// Name identifies the provider.
func (p *OpenAIProvider) Name() string {
	if p.Label != "" {
		return p.Label
	}
	return "openai-compat"
}

// NativeTools reports whether this provider handles tools natively (ToolAware).
func (p *OpenAIProvider) NativeTools() bool { return p.UseNativeTools }

// setAuth attaches the API key using the configured header (default Authorization: Bearer).
func (p *OpenAIProvider) setAuth(req *http.Request) {
	if p.APIKey == "" {
		return
	}
	if p.AuthHeader != "" {
		req.Header.Set(p.AuthHeader, p.APIKey)
		return
	}
	req.Header.Set("Authorization", "Bearer "+p.APIKey)
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

	payload := map[string]any{"model": model}
	if p.UseNativeTools {
		payload["messages"] = openAIMessages(req.Messages)
		if len(req.Tools) > 0 {
			payload["tools"] = openAITools(req.Tools)
		}
	} else {
		// Prompted path: messages are already flattened to plain text upstream.
		payload["messages"] = req.Messages
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
	p.setAuth(httpReq)

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
				Content   string           `json:"content"`
				ToolCalls []openAIToolCall `json:"tool_calls"`
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
	calls, err := parseOpenAIToolCalls(out.Choices[0].Message.ToolCalls)
	if err != nil {
		return CompletionResponse{}, fmt.Errorf("llm %s: %w", p.Name(), err)
	}
	return CompletionResponse{
		Text:         out.Choices[0].Message.Content,
		ToolCalls:    calls,
		InputTokens:  out.Usage.PromptTokens,
		OutputTokens: out.Usage.CompletionTokens,
	}, nil
}
