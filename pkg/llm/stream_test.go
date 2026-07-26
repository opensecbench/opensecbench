package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T, frames []string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = w.Write([]byte("data: " + f + "\n\n"))
		}
	}))
}

func TestAnthropicCompleteStream(t *testing.T) {
	srv := sseServer(t, []string{
		`{"type":"message_start","message":{"model":"claude-x","usage":{"input_tokens":10,"output_tokens":1}}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"tu_1","name":"get_finding"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"id\":"}}`,
		`{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\"f1\"}"}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":15}}`,
		`{"type":"message_stop"}`,
	})
	defer srv.Close()

	p := &AnthropicProvider{BaseURL: srv.URL, APIKey: "k", Model: "claude-x", UseNativeTools: true}
	var deltas []string
	out, err := p.CompleteStream(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}},
		func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if got := strings.Join(deltas, "|"); got != "Hello |world" {
		t.Fatalf("deltas = %q, want \"Hello |world\"", got)
	}
	if out.Text != "Hello world" {
		t.Fatalf("Text = %q, want %q", out.Text, "Hello world")
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Tool != "get_finding" || out.ToolCalls[0].ID != "tu_1" || out.ToolCalls[0].Args["id"] != "f1" {
		t.Fatalf("ToolCalls = %+v, want get_finding{id:f1}", out.ToolCalls)
	}
	if out.InputTokens != 10 || out.OutputTokens != 15 || out.Model != "claude-x" {
		t.Fatalf("usage/model = %d/%d/%q, want 10/15/claude-x", out.InputTokens, out.OutputTokens, out.Model)
	}
}

func TestOpenAICompleteStream(t *testing.T) {
	srv := sseServer(t, []string{
		`{"model":"gpt","choices":[{"delta":{"role":"assistant","content":"Hel"}}]}`,
		`{"choices":[{"delta":{"content":"lo"}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"get_finding","arguments":"{\"id\":"}}]}}]}`,
		`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"f1\"}"}}]}}]}`,
		`{"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":15}}`,
		`[DONE]`,
	})
	defer srv.Close()

	p := &OpenAIProvider{BaseURL: srv.URL, Model: "gpt", UseNativeTools: true}
	var deltas []string
	out, err := p.CompleteStream(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}},
		func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("CompleteStream: %v", err)
	}
	if got := strings.Join(deltas, "|"); got != "Hel|lo" {
		t.Fatalf("deltas = %q, want \"Hel|lo\"", got)
	}
	if out.Text != "Hello" {
		t.Fatalf("Text = %q, want %q", out.Text, "Hello")
	}
	if len(out.ToolCalls) != 1 || out.ToolCalls[0].Tool != "get_finding" || out.ToolCalls[0].ID != "call_1" || out.ToolCalls[0].Args["id"] != "f1" {
		t.Fatalf("ToolCalls = %+v, want get_finding{id:f1}", out.ToolCalls)
	}
	if out.InputTokens != 10 || out.OutputTokens != 15 {
		t.Fatalf("usage = %d/%d, want 10/15", out.InputTokens, out.OutputTokens)
	}
}

// A provider that can't stream still works via Stream: the whole text arrives as one delta.
func TestStreamFallbackForNonStreamingProvider(t *testing.T) {
	mock := &MockProvider{Responses: []string{"whole answer"}}
	var deltas []string
	out, err := Stream(context.Background(), mock, CompletionRequest{}, func(d string) { deltas = append(deltas, d) })
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(deltas) != 1 || deltas[0] != "whole answer" || out.Text != "whole answer" {
		t.Fatalf("deltas=%v text=%q, want one delta \"whole answer\"", deltas, out.Text)
	}
}
