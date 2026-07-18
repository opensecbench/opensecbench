package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// CLIProvider drives the `claude` CLI in headless JSON mode as an inference source (ADR-0006). Two
// things make it work as a plain completion backend rather than an agent that rejects us:
//   - the system prompt is passed as a real system prompt (--append-system-prompt), NOT flattened into
//     the user prompt — an agent CLI rightly treats an injected "you are X, here are your tools" user
//     message as a prompt injection and refuses;
//   - the CLI's own tools are disabled so it only generates text; our loop (pkg/agent) owns tool use.
//
// It uses the caller's ambient `claude` credentials (a Claude subscription or an API key). For a
// governed setup the binary runs inside a runner with only ~/.claude/.credentials.json mounted.
type CLIProvider struct {
	Bin  string
	Args []string // base args before flags; default -p
}

// disabledCLITools stops the CLI from acting as an agent — it must only return text for our loop.
var disabledCLITools = []string{
	"Bash", "Edit", "MultiEdit", "Write", "Read", "Glob", "Grep",
	"WebFetch", "WebSearch", "NotebookEdit", "Task", "TodoWrite",
}

// NewCLIProvider returns a CLI provider. Empty bin defaults to `claude`; empty args to `-p`.
func NewCLIProvider(bin string, args ...string) *CLIProvider {
	if bin == "" {
		bin = "claude"
	}
	if len(args) == 0 {
		args = []string{"-p"}
	}
	return &CLIProvider{Bin: bin, Args: args}
}

// Name identifies the provider.
func (c *CLIProvider) Name() string { return "cli:" + c.Bin }

// cliResult is the subset of `claude --output-format json` we consume.
type cliResult struct {
	IsError bool   `json:"is_error"`
	Result  string `json:"result"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Complete runs the CLI once: system prompt via flag, conversation on stdin, JSON out. The prompt goes
// on stdin because --disallowed-tools is variadic (a positional prompt would be consumed as a tool
// name); stdin also avoids arg-length limits on long conversations.
func (c *CLIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	system, convo := splitSystem(req.Messages)

	args := append([]string{}, c.Args...)
	args = append(args, "--output-format", "json")
	args = append(args, "--disallowed-tools")
	args = append(args, disabledCLITools...)
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}

	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Stdin = strings.NewReader(RenderPrompt(convo))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm cli %s: %w: %s", c.Bin, err, strings.TrimSpace(stderr.String()))
	}

	var res cliResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &res); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm cli %s: unparseable output: %w", c.Bin, err)
	}
	if res.IsError {
		return CompletionResponse{}, fmt.Errorf("llm cli %s reported an error: %s", c.Bin, res.Result)
	}
	return CompletionResponse{Text: res.Result, InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens}, nil
}

// splitSystem separates system message(s) from the user/assistant turns.
func splitSystem(msgs []Message) (system string, convo []Message) {
	var sys []string
	for _, m := range msgs {
		if m.Role == RoleSystem {
			sys = append(sys, m.Content)
			continue
		}
		convo = append(convo, m)
	}
	return strings.Join(sys, "\n\n"), convo
}

// RenderPrompt flattens user/assistant turns into a single prompt string for a CLI completion backend.
func RenderPrompt(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", strings.ToUpper(m.Role), m.Content)
	}
	return strings.TrimSpace(b.String())
}
