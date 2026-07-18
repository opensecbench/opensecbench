package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The conformance suite pins the ADR-0017 contract: every tool-aware adapter, whatever its wire
// format, must (1) turn "offered a tool, model requested it" into the same canonical ToolCall, and
// (2) turn a canonical tool history into a well-formed provider request and a canonical final answer.
// If these pass for the prompted adapter and each native adapter, a thread behaves identically across
// providers — the whole point of the canonical model.

var conformanceTools = []ToolDef{{
	Name:        "search",
	Description: "search the corpus",
	Params:      []Param{{Name: "q", Type: TypeString, Required: true, Description: "query"}},
}}

// canonical history: the model already called search and got a result; next turn it should answer.
func conformanceHistory() []Message {
	return []Message{
		{Role: RoleSystem, Content: "persona"},
		{Role: RoleUser, Content: "find acme"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{Tool: "search", Args: map[string]any{"q": "acme"}}}},
		{Role: RoleTool, Content: "results: none"},
	}
}

// assertRequestsToolCall checks the "model requested a tool" postcondition.
func assertRequestsToolCall(t *testing.T, resp CompletionResponse, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call, got %d (text=%q)", len(resp.ToolCalls), resp.Text)
	}
	c := resp.ToolCalls[0]
	if c.Tool != "search" || c.Args["q"] != "acme" {
		t.Fatalf("canonical call = %+v", c)
	}
}

// assertAnswers checks the "model gave a final answer" postcondition.
func assertAnswers(t *testing.T, resp CompletionResponse, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("want no tool calls, got %+v", resp.ToolCalls)
	}
	if resp.Text != "found it" {
		t.Fatalf("answer = %q", resp.Text)
	}
}

func TestConformancePrompted(t *testing.T) {
	call := EnsureToolAware(&captureProvider{reply: `{"tool":"search","args":{"q":"acme"}}`})
	resp, err := call.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "find acme"}}, Tools: conformanceTools})
	assertRequestsToolCall(t, resp, err)

	answer := EnsureToolAware(&captureProvider{reply: `{"answer":"found it"}`})
	resp, err = answer.Complete(context.Background(), CompletionRequest{Messages: conformanceHistory(), Tools: conformanceTools})
	assertAnswers(t, resp, err)
}

func TestConformanceAnthropicNative(t *testing.T) {
	// Model requests the tool: assert the request advertises tools, and the tool_use reply parses.
	callSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["tools"] == nil {
			t.Errorf("anthropic request missing tools: %v", body)
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"tool_use","id":"toolu_1","name":"search","input":{"q":"acme"}}],"usage":{"input_tokens":5,"output_tokens":2}}`)
	}))
	defer callSrv.Close()
	p := &AnthropicProvider{BaseURL: callSrv.URL, APIKey: "k", Model: "claude", UseNativeTools: true}
	if !p.NativeTools() {
		t.Fatal("provider should report native tools")
	}
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "find acme"}}, Tools: conformanceTools})
	assertRequestsToolCall(t, resp, err)

	// Given a tool history, assert the request carries paired tool_use/tool_result blocks, then answers.
	ansSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		useID, resultID := findAnthropicPairing(body)
		if useID == "" || useID != resultID {
			t.Errorf("tool_use id %q must equal tool_result tool_use_id %q", useID, resultID)
		}
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"found it"}],"usage":{}}`)
	}))
	defer ansSrv.Close()
	p2 := &AnthropicProvider{BaseURL: ansSrv.URL, APIKey: "k", Model: "claude", UseNativeTools: true}
	resp, err = p2.Complete(context.Background(), CompletionRequest{Messages: conformanceHistory(), Tools: conformanceTools})
	assertAnswers(t, resp, err)
}

func TestConformanceOpenAINative(t *testing.T) {
	callSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		if body["tools"] == nil {
			t.Errorf("openai request missing tools: %v", body)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_x","type":"function","function":{"name":"search","arguments":"{\"q\":\"acme\"}"}}]}}],"usage":{}}`)
	}))
	defer callSrv.Close()
	p := &OpenAIProvider{BaseURL: callSrv.URL, Model: "gpt", UseNativeTools: true}
	if !p.NativeTools() {
		t.Fatal("provider should report native tools")
	}
	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "find acme"}}, Tools: conformanceTools})
	assertRequestsToolCall(t, resp, err)

	ansSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := decodeBody(t, r)
		useID, resultID := findOpenAIPairing(body)
		if useID == "" || useID != resultID {
			t.Errorf("assistant tool_call id %q must equal tool message tool_call_id %q", useID, resultID)
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"found it"}}],"usage":{}}`)
	}))
	defer ansSrv.Close()
	p2 := &OpenAIProvider{BaseURL: ansSrv.URL, Model: "gpt", UseNativeTools: true}
	resp, err = p2.Complete(context.Background(), CompletionRequest{Messages: conformanceHistory(), Tools: conformanceTools})
	assertAnswers(t, resp, err)
}

func decodeBody(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	return m
}

// findAnthropicPairing returns the tool_use id and the tool_result tool_use_id from a request body.
func findAnthropicPairing(body map[string]any) (useID, resultID string) {
	msgs, _ := body["messages"].([]any)
	for _, mi := range msgs {
		m, _ := mi.(map[string]any)
		content, _ := m["content"].([]any)
		for _, bi := range content {
			b, _ := bi.(map[string]any)
			switch b["type"] {
			case "tool_use":
				useID, _ = b["id"].(string)
			case "tool_result":
				resultID, _ = b["tool_use_id"].(string)
			}
		}
	}
	return useID, resultID
}

// findOpenAIPairing returns the assistant tool_call id and the tool message tool_call_id.
func findOpenAIPairing(body map[string]any) (useID, resultID string) {
	msgs, _ := body["messages"].([]any)
	for _, mi := range msgs {
		m, _ := mi.(map[string]any)
		if tcs, ok := m["tool_calls"].([]any); ok && len(tcs) > 0 {
			tc, _ := tcs[0].(map[string]any)
			useID, _ = tc["id"].(string)
		}
		if m["role"] == "tool" {
			resultID, _ = m["tool_call_id"].(string)
		}
	}
	return useID, resultID
}
