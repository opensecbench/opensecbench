package llm

import (
	"context"
	"strings"
	"testing"
)

// captureProvider is a raw text backend that records the request it received and returns a fixed reply.
type captureProvider struct {
	reply string
	req   CompletionRequest
}

func (c *captureProvider) Name() string { return "capture" }
func (c *captureProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	c.req = req
	return CompletionResponse{Text: c.reply}, nil
}

func toolset() []ToolDef {
	return []ToolDef{{
		Name:        "search",
		Description: "search the corpus",
		Params:      []Param{{Name: "q", Type: TypeString, Required: true, Description: "query"}},
	}}
}

func TestPromptedInjectsToolCatalogAndParsesCall(t *testing.T) {
	raw := &captureProvider{reply: `Sure. {"tool":"search","args":{"q":"acme"}}`}
	p := EnsureToolAware(raw)

	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleSystem, Content: "You are the Analyst."}, {Role: RoleUser, Content: "find acme"}},
		Tools:    toolset(),
	})
	if err != nil {
		t.Fatal(err)
	}

	// The catalog + protocol must be folded into the existing system message, and the raw backend must
	// never see the abstract Tools (it is tool-blind).
	if len(raw.req.Tools) != 0 {
		t.Fatalf("raw backend should receive no Tools, got %d", len(raw.req.Tools))
	}
	sys := raw.req.Messages[0]
	if sys.Role != RoleSystem || !strings.Contains(sys.Content, "You are the Analyst.") {
		t.Fatalf("original system message not preserved: %+v", sys)
	}
	if !strings.Contains(sys.Content, "search: search the corpus") || !strings.Contains(sys.Content, `{"tool":`) {
		t.Fatalf("tool catalog/protocol not injected: %q", sys.Content)
	}

	// The single-JSON reply must parse into a structured call, not leak as text.
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d (text=%q)", len(resp.ToolCalls), resp.Text)
	}
	c := resp.ToolCalls[0]
	if c.Tool != "search" || c.Args["q"] != "acme" {
		t.Fatalf("parsed call = %+v", c)
	}
}

func TestPromptedParsesFinalAnswer(t *testing.T) {
	raw := &captureProvider{reply: `{"answer":"there is 1 project"}`}
	p := EnsureToolAware(raw)

	resp, err := p.Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "how many?"}},
		Tools:    toolset(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %+v", resp.ToolCalls)
	}
	if resp.Text != "there is 1 project" {
		t.Fatalf("answer text = %q", resp.Text)
	}
}

func TestEnsureToolAwareRespectsNativeAndIdempotent(t *testing.T) {
	// A native provider is returned unwrapped.
	nat := nativeProvider{}
	if _, wrapped := EnsureToolAware(nat).(*PromptedToolProvider); wrapped {
		t.Fatal("native provider must not be wrapped")
	}
	// Wrapping is idempotent — no double-wrap.
	once := EnsureToolAware(&captureProvider{})
	twice := EnsureToolAware(once)
	if once != twice {
		t.Fatal("EnsureToolAware should not double-wrap")
	}
}

type nativeProvider struct{}

func (nativeProvider) Name() string      { return "native" }
func (nativeProvider) NativeTools() bool { return true }
func (nativeProvider) Complete(_ context.Context, _ CompletionRequest) (CompletionResponse, error) {
	return CompletionResponse{}, nil
}
