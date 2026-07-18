package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// PromptedToolProvider makes a raw text-completion backend tool-capable by describing the tools in the
// prompt and parsing the model's single-JSON reply into a structured ToolCall (ADR-0017). It is the
// fallback tier for backends without native tool-use (claude-cli, plain completion). The result is the
// same canonical ToolCall / text a native adapter produces, so the agent loop is uniform.
type PromptedToolProvider struct {
	Raw Provider
}

func (p *PromptedToolProvider) Name() string { return p.Raw.Name() }

// Complete injects the tool protocol (when tools are offered), delegates to the raw backend, and parses
// the reply into a tool call or a final answer.
func (p *PromptedToolProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if len(req.Tools) > 0 {
		req.Messages = injectToolPrompt(req.Messages, req.Tools)
	}
	req.Tools = nil // the raw backend is tool-blind; the protocol is now in the messages
	resp, err := p.Raw.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	// The reply is one JSON object: a tool call or a final answer. Keep resp.Text as the raw assistant
	// turn (transcript continuity); set ToolCalls / the answer text from the parse.
	tool, answer, ok := parseReply(resp.Text)
	if ok && tool.Tool != "" {
		resp.ToolCalls = []ToolCall{tool}
	} else if ok {
		resp.Text = answer
	}
	return resp, nil
}

// injectToolPrompt appends the tool catalog + JSON protocol to the conversation's system message (or
// prepends one if absent), so the model knows what it may call and how to reply.
func injectToolPrompt(msgs []Message, tools []ToolDef) []Message {
	block := toolPromptBlock(tools)
	out := make([]Message, len(msgs))
	copy(out, msgs)
	for i := range out {
		if out[i].Role == RoleSystem {
			out[i].Content = strings.TrimRight(out[i].Content, "\n") + "\n\n" + block
			return out
		}
	}
	return append([]Message{{Role: RoleSystem, Content: block}}, out...)
}

func toolPromptBlock(tools []ToolDef) string {
	var b strings.Builder
	b.WriteString("Available tools:\n")
	for _, t := range tools {
		fmt.Fprintf(&b, "- %s: %s", t.Name, t.Description)
		if len(t.Params) > 0 {
			parts := make([]string, 0, len(t.Params))
			for _, prm := range t.Params {
				parts = append(parts, renderParam(prm))
			}
			fmt.Fprintf(&b, " [params: %s]", strings.Join(parts, "; "))
		}
		b.WriteByte('\n')
	}
	b.WriteString(`
Respond with EXACTLY ONE JSON object and nothing else — no prose, no code fences.
To call a tool: {"tool":"<name>","args":{...}}
To give your final answer (only once you have the real data from tools): {"answer":"<text>"}`)
	return b.String()
}

func renderParam(p Param) string {
	typ := string(p.Type)
	if typ == "" {
		typ = "string"
	}
	if p.Type == TypeEnum {
		typ = "one of: " + strings.Join(p.Enum, "|")
	}
	if p.Required {
		typ += ", required"
	}
	return fmt.Sprintf("%s (%s): %s", p.Name, typ, p.Description)
}

// parseReply extracts the single JSON object and returns a tool call and/or an answer.
func parseReply(text string) (call ToolCall, answer string, ok bool) {
	js := extractJSON(text)
	if js == "" {
		return ToolCall{}, "", false
	}
	var r struct {
		Tool   string         `json:"tool"`
		Args   map[string]any `json:"args"`
		Answer string         `json:"answer"`
	}
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		return ToolCall{}, "", false
	}
	return ToolCall{Tool: r.Tool, Args: r.Args}, r.Answer, true
}

// extractJSON returns the outermost {...} span in s, tolerating surrounding prose or code fences.
func extractJSON(s string) string {
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j <= i {
		return ""
	}
	return s[i : j+1]
}
