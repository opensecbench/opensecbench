package llm

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Native tool-use translation (ADR-0017). These helpers convert the canonical, vendor-neutral tool
// schema and tool turns into the two dominant wire formats — Anthropic's tool_use/tool_result content
// blocks and OpenAI's tool_calls / role:"tool" messages — and parse each back into canonical form. A
// provider opts into its native path; otherwise the prompted adapter handles tools as text.

// paramsToSchema renders a tool's parameters as a JSON-Schema object (the shape both Anthropic's
// input_schema and OpenAI's function.parameters expect).
func paramsToSchema(params []Param) map[string]any {
	props := map[string]any{}
	var required []string
	for _, p := range params {
		props[p.Name] = paramSchema(p)
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func paramSchema(p Param) map[string]any {
	s := map[string]any{}
	if p.Description != "" {
		s["description"] = p.Description
	}
	switch p.Type {
	case TypeEnum:
		s["type"] = "string"
		s["enum"] = p.Enum
	case TypeInteger:
		s["type"] = "integer"
	case TypeNumber:
		s["type"] = "number"
	case TypeBoolean:
		s["type"] = "boolean"
	case TypeArray:
		s["type"] = "array"
	case TypeObject:
		s["type"] = "object"
	default:
		s["type"] = "string"
	}
	return s
}

// normalizeToolIDs returns a copy of msgs with a stable id on every tool call and its matching result.
// Native APIs require the assistant's tool_use/tool_call id to equal the following tool_result/tool
// message id. A thread that originated on the prompted path has empty ids; we synthesize positional
// ones ("call_1", "call_2", …) so it ports cleanly to a native provider. Ids already present (from a
// prior native round-trip) are preserved, so a thread can move between native providers too.
func normalizeToolIDs(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	copy(out, msgs)
	seq := 0
	var pending string // id of the most recent tool call awaiting its result
	for i := range out {
		switch {
		case out[i].Role == RoleAssistant && len(out[i].ToolCalls) > 0:
			calls := make([]ToolCall, len(out[i].ToolCalls))
			copy(calls, out[i].ToolCalls)
			for j := range calls {
				if calls[j].ID == "" {
					seq++
					calls[j].ID = "call_" + strconv.Itoa(seq)
				}
				pending = calls[j].ID // last call id (single-call-per-turn today)
			}
			out[i].ToolCalls = calls
		case out[i].Role == RoleTool && out[i].ToolCallID == "":
			out[i].ToolCallID = pending
		}
	}
	return out
}

func nonNilArgs(args map[string]any) map[string]any {
	if args == nil {
		return map[string]any{}
	}
	return args
}

// encodeArgs marshals tool-call arguments to a JSON object string (OpenAI's arguments field).
func encodeArgs(args map[string]any) string {
	b, err := json.Marshal(nonNilArgs(args))
	if err != nil {
		return "{}"
	}
	return string(b)
}

// decodeArgs parses an arguments JSON string back to a map (empty string → empty map).
func decodeArgs(s string) (map[string]any, error) {
	if strings.TrimSpace(s) == "" {
		return map[string]any{}, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// --- Anthropic native (Messages API tool_use / tool_result content blocks) ---

func anthropicTools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": paramsToSchema(t.Params),
		})
	}
	return out
}

// anthropicMessages splits out the system prompt and renders the rest as Anthropic message objects,
// translating canonical tool turns into tool_use / tool_result content blocks.
func anthropicMessages(msgs []Message) (system string, out []map[string]any) {
	msgs = normalizeToolIDs(msgs)
	for _, m := range msgs {
		switch {
		case m.Role == RoleSystem:
			system += m.Content + "\n"
		case m.Role == RoleTool:
			out = append(out, map[string]any{"role": "user", "content": []map[string]any{{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
				"content":     m.Content,
				"is_error":    m.ToolError,
			}}})
		case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
			content := make([]map[string]any, 0, len(m.ToolCalls)+1)
			if m.Content != "" {
				content = append(content, map[string]any{"type": "text", "text": m.Content})
			}
			for _, c := range m.ToolCalls {
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    c.ID,
					"name":  c.Tool,
					"input": nonNilArgs(c.Args),
				})
			}
			out = append(out, map[string]any{"role": "assistant", "content": content})
		default:
			role := m.Role
			if role != RoleUser && role != RoleAssistant {
				role = RoleUser
			}
			out = append(out, map[string]any{"role": role, "content": m.Content})
		}
	}
	return system, out
}

// anthropicContentBlock is one element of a Messages API response content array.
type anthropicContentBlock struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// parseAnthropicContent turns a response content array into canonical text + tool calls.
func parseAnthropicContent(blocks []anthropicContentBlock) (text string, calls []ToolCall) {
	for _, b := range blocks {
		switch b.Type {
		case "text":
			text += b.Text
		case "tool_use":
			calls = append(calls, ToolCall{ID: b.ID, Tool: b.Name, Args: b.Input})
		}
	}
	return text, calls
}

// --- OpenAI native (chat/completions tool_calls / role:"tool" messages) ---

func openAITools(tools []ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  paramsToSchema(t.Params),
			},
		})
	}
	return out
}

// openAIMessages renders the canonical conversation as OpenAI message objects, translating tool turns
// into assistant tool_calls and role:"tool" result messages.
func openAIMessages(msgs []Message) []map[string]any {
	msgs = normalizeToolIDs(msgs)
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		switch {
		case m.Role == RoleTool:
			out = append(out, map[string]any{"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content})
		case m.Role == RoleAssistant && len(m.ToolCalls) > 0:
			tcs := make([]map[string]any, 0, len(m.ToolCalls))
			for _, c := range m.ToolCalls {
				tcs = append(tcs, map[string]any{
					"id":   c.ID,
					"type": "function",
					"function": map[string]any{
						"name":      c.Tool,
						"arguments": encodeArgs(c.Args), // OpenAI wants arguments as a JSON-encoded string
					},
				})
			}
			msg := map[string]any{"role": "assistant", "tool_calls": tcs}
			if m.Content != "" {
				msg["content"] = m.Content
			}
			out = append(out, msg)
		default:
			role := m.Role
			if role != RoleUser && role != RoleAssistant {
				role = RoleUser
			}
			out = append(out, map[string]any{"role": role, "content": m.Content})
		}
	}
	return out
}

// openAIToolCall is one element of a chat/completions response tool_calls array.
type openAIToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"` // a JSON-encoded string
	} `json:"function"`
}

func parseOpenAIToolCalls(raw []openAIToolCall) ([]ToolCall, error) {
	out := make([]ToolCall, 0, len(raw))
	for _, tc := range raw {
		args, err := decodeArgs(tc.Function.Arguments)
		if err != nil {
			return nil, fmt.Errorf("tool %q arguments: %w", tc.Function.Name, err)
		}
		out = append(out, ToolCall{ID: tc.ID, Tool: tc.Function.Name, Args: args})
	}
	return out, nil
}
