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

// buildRequest assembles the chat/completions HTTP request (payload + auth). stream adds "stream": true and
// asks for usage in the final chunk, so the same builder serves Complete and CompleteStream.
func (p *OpenAIProvider) buildRequest(ctx context.Context, req CompletionRequest, stream bool) (*http.Request, error) {
	if p.BaseURL == "" {
		return nil, errors.New("llm openai: base URL not set")
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
	if stream {
		payload["stream"] = true
		// Ask for usage in the terminal chunk so streamed calls still report tokens (OpenAI + most compat
		// servers honor this; a server that ignores it just yields usage 0, which degrades accounting, not the run).
		payload["stream_options"] = map[string]any{"include_usage": true}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	p.setAuth(httpReq)
	return httpReq, nil
}

func (p *OpenAIProvider) client() *http.Client {
	if p.HTTP != nil {
		return p.HTTP
	}
	return http.DefaultClient
}

// Complete calls the chat/completions endpoint.
func (p *OpenAIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, false)
	if err != nil {
		return CompletionResponse{}, err
	}
	resp, err := p.client().Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm %s: %s: %s", p.Name(), resp.Status, string(b))
	}

	var out struct {
		Model   string `json:"model"`
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
		Model:        out.Model,
	}, nil
}

// CompleteStream calls chat/completions with streaming on, firing onDelta for each content chunk and
// assembling the same full CompletionResponse. Tool calls arrive as fragments (id/name first, arguments in
// pieces) keyed by index; they're accumulated and parsed once the stream ends.
func (p *OpenAIProvider) CompleteStream(ctx context.Context, req CompletionRequest, onDelta StreamHandler) (CompletionResponse, error) {
	httpReq, err := p.buildRequest(ctx, req, true)
	if err != nil {
		return CompletionResponse{}, err
	}
	resp, err := p.client().Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm %s: %s: %s", p.Name(), resp.Status, string(b))
	}

	type partial struct {
		id, name string
		args     strings.Builder
	}
	calls := map[int]*partial{}
	var order []int
	var out CompletionResponse

	err = sseData(resp.Body, func(data string) bool {
		if data == "[DONE]" {
			return false
		}
		var ev struct {
			Model   string `json:"model"`
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true // skip a malformed frame
		}
		if ev.Model != "" {
			out.Model = ev.Model
		}
		if ev.Usage != nil {
			out.InputTokens = ev.Usage.PromptTokens
			out.OutputTokens = ev.Usage.CompletionTokens
		}
		for _, ch := range ev.Choices {
			if ch.Delta.Content != "" {
				out.Text += ch.Delta.Content
				onDelta(ch.Delta.Content)
			}
			for _, t := range ch.Delta.ToolCalls {
				c := calls[t.Index]
				if c == nil {
					c = &partial{}
					calls[t.Index] = c
					order = append(order, t.Index)
				}
				if t.ID != "" {
					c.id = t.ID
				}
				if t.Function.Name != "" {
					c.name = t.Function.Name
				}
				c.args.WriteString(t.Function.Arguments)
			}
		}
		return true
	})
	if err != nil {
		return out, err
	}
	for _, idx := range order {
		c := calls[idx]
		args, derr := decodeArgs(c.args.String())
		if derr != nil {
			return out, fmt.Errorf("llm %s: tool %q arguments: %w", p.Name(), c.name, derr)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: c.id, Tool: c.name, Args: args})
	}
	return out, nil
}
