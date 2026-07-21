package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestConfigSelectsProvider(t *testing.T) {
	cases := map[string]string{
		"mock":       "mock",
		"claude-cli": "claude-subscription", // now a native Anthropic provider on the subscription OAuth token
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

func TestFromEnvNativeToolsDefault(t *testing.T) {
	// Native tool-use is on by default in the config path; OSB_LLM_NATIVE_TOOLS=0 forces prompted.
	t.Setenv("OSB_LLM_PROVIDER", "anthropic")

	t.Setenv("OSB_LLM_NATIVE_TOOLS", "")
	p, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if ta, ok := p.(ToolAware); !ok || !ta.NativeTools() {
		t.Fatal("native tools should be ON by default")
	}

	t.Setenv("OSB_LLM_NATIVE_TOOLS", "0")
	p, err = FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if ta, ok := p.(ToolAware); ok && ta.NativeTools() {
		t.Fatal("OSB_LLM_NATIVE_TOOLS=0 should force the prompted fallback")
	}
}
