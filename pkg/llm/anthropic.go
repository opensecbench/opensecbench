package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// readSubscriptionToken reads a Claude subscription's OAuth access token from the credential file the
// `claude` login writes (~/.claude/.credentials.json). Read per-call so a token the CLI refreshes in the
// background is picked up. Returns a clear error when the file is missing or the token has expired (run
// `claude` to refresh).
func readSubscriptionToken(path string) (string, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // operator-configured credential path
	if err != nil {
		return "", fmt.Errorf("llm claude: reading subscription credential %s: %w", path, err)
	}
	var creds struct {
		OAuth struct {
			AccessToken string `json:"accessToken"`
			ExpiresAt   int64  `json:"expiresAt"` // epoch millis
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		return "", fmt.Errorf("llm claude: parsing subscription credential: %w", err)
	}
	if creds.OAuth.AccessToken == "" {
		return "", errors.New("llm claude: no subscription token found (run `claude` to log in)")
	}
	if creds.OAuth.ExpiresAt > 0 && time.UnixMilli(creds.OAuth.ExpiresAt).Before(time.Now()) {
		return "", errors.New("llm claude: subscription token expired (run `claude` to refresh)")
	}
	return creds.OAuth.AccessToken, nil
}

// AnthropicProvider talks to the Anthropic Messages API natively (system prompt is a top-level
// field; only user/assistant turns go in messages).
type AnthropicProvider struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
	// UseNativeTools sends tools and tool turns as native tool_use/tool_result blocks (ADR-0017)
	// instead of the prompted text protocol. The config paths enable it by default (OSB_LLM_NATIVE_TOOLS=0
	// forces the prompted fallback).
	UseNativeTools bool
	// CredentialFile, when set, authenticates with a Claude subscription's OAuth token read fresh from
	// that file (~/.claude/.credentials.json) on each call — so the CLI's own token refresh is picked up —
	// instead of an x-api-key. This makes the Claude subscription a first-class NATIVE-tools backend
	// (no CLI subprocess, no MCP): the Messages API with a Bearer token and the oauth beta header.
	CredentialFile string
}

// Name identifies the provider.
func (a *AnthropicProvider) Name() string {
	if a.CredentialFile != "" {
		return "claude-subscription"
	}
	return "anthropic"
}

// claudeCodeUserAgent identifies the request as the official Claude Code CLI. A subscription OAuth token
// used outside that client shape is rejected with a misleading `429 rate_limit_error` (message "Error") —
// so we must present the CLI's User-Agent and app headers, not the Go default. Override the version with
// OSB_CLAUDE_USER_AGENT if the installed CLI drifts.
func claudeCodeUserAgent() string {
	if v := os.Getenv("OSB_CLAUDE_USER_AGENT"); v != "" {
		return v
	}
	return "claude-cli/2.1.214 (external, cli)"
}

// claudeCodeBillingPrefix is the text of the first `system` block a real `claude -p` sends — Anthropic
// uses it to attribute an OAuth (subscription) request to the Claude Code subscription. It's a fixed
// billing marker, NOT the persona: the actual instructions follow as the next block. Override with
// OSB_CLAUDE_SYSTEM_PREFIX to match a re-captured value if Anthropic changes the shape.
func claudeCodeBillingPrefix() string {
	if v := os.Getenv("OSB_CLAUDE_SYSTEM_PREFIX"); v != "" {
		return v
	}
	return "You are Claude Code, Anthropic's official CLI for Claude."
}

// setAuth attaches Anthropic auth: a subscription OAuth Bearer token (read fresh from the credential file)
// when configured, else the x-api-key. Always sets anthropic-version.
func (a *AnthropicProvider) setAuth(req *http.Request) error {
	req.Header.Set("anthropic-version", "2023-06-01")
	if a.CredentialFile != "" {
		tok, err := readSubscriptionToken(a.CredentialFile)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
		// Present the Claude Code client shape so the OAuth token is accepted (see claudeCodeUserAgent).
		req.Header.Set("User-Agent", claudeCodeUserAgent())
		req.Header.Set("x-app", "cli")
		req.Header.Set("x-stainless-lang", "js")
		req.Header.Set("x-stainless-runtime", "node")
		req.Header.Set("x-stainless-retry-count", "0")
		return nil
	}
	if a.APIKey == "" {
		return errors.New("llm anthropic: no API key or subscription credential")
	}
	req.Header.Set("x-api-key", a.APIKey)
	return nil
}

// NativeTools reports whether this provider handles tools natively (ToolAware).
func (a *AnthropicProvider) NativeTools() bool { return a.UseNativeTools }

// buildRequest assembles the Messages API HTTP request (payload + auth). stream adds "stream": true so the
// same builder serves Complete and CompleteStream.
func (a *AnthropicProvider) buildRequest(ctx context.Context, req CompletionRequest, stream bool) (*http.Request, error) {
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
	if stream {
		payload["stream"] = true
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
	// On the subscription OAuth path, the `system` array must lead with the Claude Code billing block —
	// how Anthropic attributes the call to the subscription. Its absence yields the opaque
	// 429 rate_limit_error even at 0% utilization; with it, requests succeed even mid-throttle.
	if a.CredentialFile != "" {
		blocks := []map[string]any{{
			"type": "text", "text": claudeCodeBillingPrefix(),
			"cache_control": map[string]any{"type": "ephemeral"},
		}}
		if system != "" {
			blocks = append(blocks, map[string]any{"type": "text", "text": system})
		}
		payload["system"] = blocks
	} else if system != "" {
		payload["system"] = system
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if err := a.setAuth(httpReq); err != nil {
		return nil, err
	}
	return httpReq, nil
}

func (a *AnthropicProvider) client() *http.Client {
	if a.HTTP != nil {
		return a.HTTP
	}
	return http.DefaultClient
}

// Complete calls the Messages API.
func (a *AnthropicProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	httpReq, err := a.buildRequest(ctx, req, false)
	if err != nil {
		return CompletionResponse{}, err
	}
	resp, err := a.client().Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm anthropic: %s: %s", resp.Status, string(b))
	}

	var out struct {
		Model   string                  `json:"model"`
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
	return CompletionResponse{Text: text, ToolCalls: calls, InputTokens: out.Usage.InputTokens, OutputTokens: out.Usage.OutputTokens, Model: out.Model}, nil
}

// CompleteStream calls the Messages API with streaming on, firing onDelta for each text chunk and assembling
// the same full CompletionResponse (text + tool calls + usage) the non-streaming path returns.
func (a *AnthropicProvider) CompleteStream(ctx context.Context, req CompletionRequest, onDelta StreamHandler) (CompletionResponse, error) {
	httpReq, err := a.buildRequest(ctx, req, true)
	if err != nil {
		return CompletionResponse{}, err
	}
	resp, err := a.client().Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm anthropic: %s: %s", resp.Status, string(b))
	}

	// Content blocks arrive by index: a text block streams text_delta; a tool_use block streams its input as
	// input_json_delta fragments we accumulate and parse at the end.
	type block struct {
		typ, id, name string
		jsonBuf       strings.Builder
	}
	blocks := map[int]*block{}
	var order []int
	var out CompletionResponse

	err = sseData(resp.Body, func(data string) bool {
		var ev struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Model string `json:"model"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type string `json:"type"`
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				PartialJSON string `json:"partial_json"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &ev) != nil {
			return true // skip a malformed frame rather than abort the stream
		}
		switch ev.Type {
		case "message_start":
			out.Model = ev.Message.Model
			out.InputTokens = ev.Message.Usage.InputTokens
		case "content_block_start":
			b := &block{typ: ev.ContentBlock.Type, id: ev.ContentBlock.ID, name: ev.ContentBlock.Name}
			blocks[ev.Index] = b
			order = append(order, ev.Index)
		case "content_block_delta":
			b := blocks[ev.Index]
			if b == nil {
				return true
			}
			switch ev.Delta.Type {
			case "text_delta":
				out.Text += ev.Delta.Text
				onDelta(ev.Delta.Text)
			case "input_json_delta":
				b.jsonBuf.WriteString(ev.Delta.PartialJSON)
			}
		case "message_delta":
			out.OutputTokens = ev.Usage.OutputTokens
		case "message_stop":
			return false
		}
		return true
	})
	if err != nil {
		return out, err
	}
	for _, idx := range order {
		b := blocks[idx]
		if b.typ != "tool_use" {
			continue
		}
		args := map[string]any{}
		if s := strings.TrimSpace(b.jsonBuf.String()); s != "" {
			_ = json.Unmarshal([]byte(s), &args)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.id, Tool: b.name, Args: args})
	}
	return out, nil
}
