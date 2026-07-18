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
	// UseNativeTools sends tools and tool turns as native tool_use/tool_result blocks (ADR-0017)
	// instead of the prompted text protocol. Off by default: the prompted path is the proven one.
	UseNativeTools bool
}

// Name identifies the provider.
func (a *AnthropicProvider) Name() string { return "anthropic" }

// NativeTools reports whether this provider handles tools natively (ToolAware).
func (a *AnthropicProvider) NativeTools() bool { return a.UseNativeTools }

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

	payload := map[string]any{
		"model":      model,
		"max_tokens": maxTokens,
	}
	var system string
	if a.UseNativeTools {
		// Native path: translate canonical tool turns into tool_use/tool_result content blocks.
		var msgs []map[string]any
		system, msgs = anthropicMessages(req.Messages)
		payload["messages"] = msgs
		if len(req.Tools) > 0 {
			payload["tools"] = anthropicTools(req.Tools)
		}
	} else {
		// Prompted path: messages are already flattened to plain text upstream.
		msgs := make([]Message, 0, len(req.Messages))
		for _, m := range req.Messages {
			if m.Role == RoleSystem {
				system += m.Content + "\n"
				continue
			}
			msgs = append(msgs, m)
		}
		payload["messages"] = msgs
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
		Content []anthropicContentBlock `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CompletionResponse{}, err
	}
	text, calls := parseAnthropicContent(out.Content)
	return CompletionResponse{Text: text, ToolCalls: calls, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens}, nil
}
