package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAIProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			t.Errorf("missing/wrong auth header: %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["model"] != "deepseek-chat" {
			t.Errorf("model = %v", body["model"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "hello from provider"}}},
			"usage":   map[string]int{"prompt_tokens": 10, "completion_tokens": 3},
		})
	}))
	defer srv.Close()

	p := &OpenAIProvider{Label: "deepseek", BaseURL: srv.URL, APIKey: "sk-test", Model: "deepseek-chat"}
	resp, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hello from provider" || resp.InputTokens != 10 || resp.OutputTokens != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestAnthropicProvider(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "key" {
			t.Errorf("bad request: %s %q", r.URL.Path, r.Header.Get("x-api-key"))
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["system"] == nil {
			t.Error("system prompt not lifted to top level")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []map[string]string{{"type": "text", "text": "answer"}},
			"usage":   map[string]int{"input_tokens": 5, "output_tokens": 2},
		})
	}))
	defer srv.Close()

	p := &AnthropicProvider{BaseURL: srv.URL, APIKey: "key", Model: "claude-sonnet-5"}
	resp, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: "be terse"},
		{Role: RoleUser, Content: "hi"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "answer" || resp.OutputTokens != 2 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestCLIProvider(t *testing.T) {
	// `echo` stands in for the completion binary and echoes the rendered prompt.
	p := &CLIProvider{Bin: "echo"}
	resp, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hello world"}}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "hello world") {
		t.Fatalf("cli output missing prompt: %q", resp.Text)
	}
}

func TestConfigSelectsProvider(t *testing.T) {
	cases := map[string]string{
		"mock":       "mock",
		"claude-cli": "cli:claude",
		"ollama":     "ollama",
		"deepseek":   "deepseek",
		"grok":       "grok",
	}
	for typ, wantName := range cases {
		p, err := New(Config{Type: typ})
		if err != nil {
			t.Fatalf("New(%s): %v", typ, err)
		}
		if p.Name() != wantName {
			t.Errorf("New(%s).Name() = %s, want %s", typ, p.Name(), wantName)
		}
	}
	if _, err := New(Config{Type: "nope"}); err == nil {
		t.Error("expected error for unknown provider")
	}
}
