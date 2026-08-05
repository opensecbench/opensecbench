package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// CLIProvider drives the `claude` CLI in headless JSON mode as an inference source (ADR-0006). Two
// things make it work as a plain completion backend rather than an agent that rejects us:
//   - the system prompt is passed as a real system prompt (--append-system-prompt), NOT flattened into
//     the user prompt — an agent CLI rightly treats an injected "you are X, here are your tools" user
//     message as a prompt injection and refuses;
//   - the CLI's own tools are disabled so it only generates text; our loop (pkg/agent) owns tool use.
//
// It uses the caller's ambient `claude` credentials (a Claude subscription or an API key). For a
// governed setup, set Sandbox: the binary then runs inside a runner with only the credential file
// mounted (ADR-0018).
type CLIProvider struct {
	Bin  string
	Args []string // base args before flags; default -p
	// Model, when set, is the connection's default model, passed as `--model` (ADR-0052). A per-request
	// req.Model (e.g. from tag routing) overrides it. Empty → the CLI's own default model. This lets a
	// subscription connection run the same Anthropic models a direct API connection does.
	Model string
	// Sandbox, when set, runs the CLI inside a runner container mounting only the credential file,
	// instead of exec'ing it on the host (ADR-0018). Nil → direct host exec (the default).
	Sandbox *CLISandbox
}

// CLISandbox configures running the CLI inside a runner container with only the credential file
// exposed (ADR-0018). It reaches the network (egress) because the CLI must call the Anthropic API.
type CLISandbox struct {
	Runner runner.Runner
	Image  string // container image that has the `claude` CLI installed
	// CredentialSrc is the host path mounted read-only to <HomeDir>/.claude/.credentials.json.
	CredentialSrc string
	HomeDir       string        // container HOME (default /root)
	Network       string        // egress network (default "bridge")
	Timeout       time.Duration // default 120s
}

// DefaultCLIImage is the sandbox image used when OSB_LLM_CLI_IMAGE is unset — published multi-arch to GHCR
// so it's pullable on any host. Pinned to the CLAUDE_VERSION in images/claude-cli/Dockerfile (keep in step
// when bumping). Override the env var to use a locally-built tag (`make claude-image` → osb/claude-cli:latest)
// or a private mirror.
const DefaultCLIImage = "ghcr.io/opensecbench/claude-cli:2.1.222"

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

// cliResult is the subset of `claude --output-format json` we consume. ModelUsage is keyed by the model
// id(s) that actually served the request.
type cliResult struct {
	IsError    bool                       `json:"is_error"`
	Result     string                     `json:"result"`
	Model      string                     `json:"model"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// servedModel returns the model the CLI reports actually ran: the top-level `model`, else the (single)
// key of modelUsage. Empty if the CLI reported neither.
func (r cliResult) servedModel() string {
	if r.Model != "" {
		return r.Model
	}
	for id := range r.ModelUsage {
		return id
	}
	return ""
}

// Complete runs the CLI once: system prompt via flag, conversation on stdin, JSON out. The prompt goes
// on stdin because --disallowed-tools is variadic (a positional prompt would be consumed as a tool
// name); stdin also avoids arg-length limits on long conversations. It runs on the host by default, or
// inside a runner container when Sandbox is set (ADR-0018) — both paths produce the same JSON.
func (c *CLIProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	system, convo := splitSystem(req.Messages)

	model := req.Model
	if model == "" {
		model = c.Model
	}
	args := c.buildArgs(system, model)
	stdin := []byte(RenderPrompt(convo))

	var stdout []byte
	var err error
	if c.Sandbox != nil {
		stdout, err = c.Sandbox.run(ctx, c.Bin, args, stdin)
	} else {
		stdout, err = c.runHost(ctx, args, stdin)
	}
	if err != nil {
		return CompletionResponse{}, err
	}
	return c.parseResult(stdout)
}

// buildArgs assembles the CLI flags: JSON output, disabled agent tools, the system prompt as a real
// system prompt, and (when set) the model. The prompt itself goes on stdin, not here.
func (c *CLIProvider) buildArgs(system, model string) []string {
	args := append([]string{}, c.Args...)
	args = append(args, "--output-format", "json")
	args = append(args, "--disallowed-tools")
	args = append(args, disabledCLITools...)
	if model != "" {
		args = append(args, "--model", model)
	}
	if system != "" {
		args = append(args, "--append-system-prompt", system)
	}
	return args
}

// runHost execs the CLI directly on the host, returning its stdout.
func (c *CLIProvider) runHost(ctx context.Context, args []string, stdin []byte) ([]byte, error) {
	cmd := exec.CommandContext(ctx, c.Bin, args...)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("llm cli %s: %w: %s", c.Bin, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

// run executes the CLI inside a runner container, mounting only the credential file, and returns its
// stdout. It reaches the network because the CLI must call the Anthropic API.
func (s *CLISandbox) run(ctx context.Context, bin string, args []string, stdin []byte) ([]byte, error) {
	home := s.HomeDir
	if home == "" {
		home = "/root"
	}
	network := s.Network
	if network == "" {
		network = "bridge"
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	res, err := s.Runner.Run(ctx, runner.RunSpec{
		Image:   s.Image,
		Cmd:     append([]string{bin}, args...),
		Stdin:   stdin,
		Env:     []string{"HOME=" + home},
		Network: network,
		Timeout: timeout,
		Mounts: []runner.Mount{{
			Source:   s.CredentialSrc,
			Target:   home + "/.claude/.credentials.json",
			ReadOnly: true,
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("llm cli sandbox: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("llm cli sandbox: exit %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

// parseResult decodes `claude --output-format json` stdout into a completion.
func (c *CLIProvider) parseResult(stdout []byte) (CompletionResponse, error) {
	var res cliResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout), &res); err != nil {
		return CompletionResponse{}, fmt.Errorf("llm cli %s: unparseable output: %w", c.Bin, err)
	}
	if res.IsError {
		return CompletionResponse{}, fmt.Errorf("llm cli %s reported an error: %s", c.Bin, res.Result)
	}
	return CompletionResponse{Text: res.Result, InputTokens: res.Usage.InputTokens, OutputTokens: res.Usage.OutputTokens, Model: res.servedModel()}, nil
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
