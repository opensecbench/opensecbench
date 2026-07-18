package llm

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// CLIProvider runs a configurable binary for a one-shot completion (default: `claude -p`). We use
// the binary only as an inference source — the agent loop stays ours (ADR-0006). The rendered
// prompt is passed as the final argument.
type CLIProvider struct {
	Bin  string
	Args []string
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

// Complete flattens the messages into a prompt and runs the binary, capturing stdout as the reply.
func (c *CLIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	prompt := RenderPrompt(req.Messages)
	args := append(append([]string{}, c.Args...), prompt)

	cmd := exec.CommandContext(ctx, c.Bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm cli %s: %w: %s", c.Bin, err, strings.TrimSpace(stderr.String()))
	}
	return CompletionResponse{Text: strings.TrimSpace(stdout.String())}, nil
}

// RenderPrompt flattens a message list into a single plain-text prompt for a completion backend
// that takes one string (a CLI binary).
func RenderPrompt(msgs []Message) string {
	var b strings.Builder
	for _, m := range msgs {
		fmt.Fprintf(&b, "[%s]\n%s\n\n", strings.ToUpper(m.Role), m.Content)
	}
	return strings.TrimSpace(b.String())
}
