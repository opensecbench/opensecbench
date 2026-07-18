// Package agent is the tool-calling loop that drives the Analyst (ADR-0006). It is provider-
// agnostic: it uses structured tool-prompting (the model replies with a single JSON object — a
// tool call or a final answer) so any inference backend works. Every tool call passes an approval
// gate and is audited; the loop never gives the model a raw host shell.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

// Tool is a capability the Analyst may call, as advertised to the model.
type Tool struct {
	Name        string
	Description string
	Params      map[string]string // param name -> human description
}

// ToolCall is a requested tool invocation.
type ToolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
}

// Step records one tool interaction in a run.
type Step struct {
	Call     ToolCall `json:"call"`
	Approved bool     `json:"approved"`
	Result   string   `json:"result,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// Result is the outcome of a run.
type Result struct {
	Answer     string        `json:"answer"`
	Steps      []Step        `json:"steps"`
	Transcript []llm.Message `json:"transcript"`
}

// Loop runs the Analyst's reason-act cycle.
type Loop struct {
	Provider llm.Provider
	Tools    []Tool
	// Approve gates a tool call; nil auto-approves. Return false to deny.
	Approve func(ctx context.Context, call ToolCall) (bool, error)
	// Execute runs an approved tool call and returns its textual result.
	Execute func(ctx context.Context, call ToolCall) (string, error)
	// Audit, if set, records loop events (action, detail).
	Audit     func(action, detail string)
	MaxSteps  int
	Model     string
	MaxTokens int
}

// Run drives the loop from a user message until the model answers or the step cap is reached.
func (l *Loop) Run(ctx context.Context, userMessage string) (Result, error) {
	maxSteps := l.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 8
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: buildSystemPrompt(l.Tools)},
		{Role: llm.RoleUser, Content: userMessage},
	}
	var res Result

	for step := 0; step < maxSteps; step++ {
		resp, err := l.Provider.Complete(ctx, llm.CompletionRequest{Messages: msgs, Model: l.Model, MaxTokens: l.MaxTokens})
		if err != nil {
			return res, err
		}
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: resp.Text})

		rep, ok := parseReply(resp.Text)
		if !ok || rep.Tool == "" {
			res.Answer = rep.Answer
			if res.Answer == "" {
				res.Answer = strings.TrimSpace(resp.Text)
			}
			res.Transcript = msgs
			return res, nil
		}

		call := ToolCall{Tool: rep.Tool, Args: rep.Args}
		l.audit("agent.tool.proposed", call.Tool)
		st := Step{Call: call}

		approved := true
		if l.Approve != nil {
			approved, err = l.Approve(ctx, call)
			if err != nil {
				return res, err
			}
		}
		st.Approved = approved
		if !approved {
			l.audit("agent.tool.denied", call.Tool)
			st.Result = "(denied)"
			res.Steps = append(res.Steps, st)
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q was denied by the human. Do not retry it; continue or give your final answer.", call.Tool)})
			continue
		}

		out, execErr := l.Execute(ctx, call)
		if execErr != nil {
			st.Error = execErr.Error()
			l.audit("agent.tool.error", call.Tool)
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q errored: %s", call.Tool, execErr.Error())})
		} else {
			st.Result = out
			l.audit("agent.tool.executed", call.Tool)
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("Tool %q result:\n%s", call.Tool, out)})
		}
		res.Steps = append(res.Steps, st)
	}

	res.Transcript = msgs
	res.Answer = "(stopped: reached the step limit without a final answer)"
	return res, nil
}

func (l *Loop) audit(action, detail string) {
	if l.Audit != nil {
		l.Audit(action, detail)
	}
}

func buildSystemPrompt(tools []Tool) string {
	var b strings.Builder
	b.WriteString("You are the Analyst, an application security assessment assistant. ")
	b.WriteString("You help review evidence and, when useful, call tools. You never have a raw shell.\n\n")
	if len(tools) > 0 {
		b.WriteString("Available tools:\n")
		for _, t := range tools {
			fmt.Fprintf(&b, "- %s: %s", t.Name, t.Description)
			if len(t.Params) > 0 {
				parts := make([]string, 0, len(t.Params))
				for name, desc := range t.Params {
					parts = append(parts, fmt.Sprintf("%s (%s)", name, desc))
				}
				fmt.Fprintf(&b, " [params: %s]", strings.Join(parts, ", "))
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(`Respond with EXACTLY ONE JSON object and nothing else.
To call a tool: {"tool":"<name>","args":{...}}
To give your final answer: {"answer":"<text>"}`)
	return b.String()
}

type reply struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Answer string         `json:"answer"`
}

func parseReply(text string) (reply, bool) {
	js := extractJSON(text)
	if js == "" {
		return reply{}, false
	}
	var r reply
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		return reply{}, false
	}
	return r, true
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
