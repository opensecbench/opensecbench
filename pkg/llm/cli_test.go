package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// fakeClaude writes a stub `claude` that records its args and stdin to files and emits a JSON
// envelope, so the adapter's wiring can be tested without the real CLI or credentials.
func fakeClaude(t *testing.T, resultJSON string) (bin, argsFile, stdinFile string) {
	t.Helper()
	dir := t.TempDir()
	argsFile = filepath.Join(dir, "args")
	stdinFile = filepath.Join(dir, "stdin")
	bin = filepath.Join(dir, "fakeclaude")
	script := "#!/bin/sh\ncat > \"" + stdinFile + "\"\necho \"$@\" > \"" + argsFile + "\"\nprintf '%s' '" + resultJSON + "'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile, stdinFile
}

func TestCLIProviderPassesSystemAsFlagAndConvoOnStdin(t *testing.T) {
	bin, argsFile, stdinFile := fakeClaude(t, `{"is_error":false,"result":"OK","usage":{"input_tokens":3,"output_tokens":4}}`)

	resp, err := NewCLIProvider(bin).Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: "SYSPROMPT"},
		{Role: RoleUser, Content: "hello there"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "OK" || resp.InputTokens != 3 || resp.OutputTokens != 4 {
		t.Fatalf("parsed response = %+v, want OK / 3 / 4", resp)
	}

	args, _ := os.ReadFile(argsFile)
	stdin, _ := os.ReadFile(stdinFile)
	// The system prompt must go through the flag (authoritative), never flattened into user text.
	if !strings.Contains(string(args), "--append-system-prompt SYSPROMPT") {
		t.Fatalf("system prompt not passed via flag; args = %q", args)
	}
	if !strings.Contains(string(args), "--output-format json") {
		t.Fatalf("json output not requested; args = %q", args)
	}
	if !strings.Contains(string(args), "--disallowed-tools") {
		t.Fatalf("CLI tools not disabled; args = %q", args)
	}
	if strings.Contains(string(stdin), "SYSPROMPT") {
		t.Fatalf("system prompt leaked into stdin/user text: %q", stdin)
	}
	if !strings.Contains(string(stdin), "hello there") {
		t.Fatalf("conversation not sent on stdin: %q", stdin)
	}
}

// TestCLIProviderReal exercises the adapter against the real `claude` binary + ambient credentials.
// Skipped by default (CI has neither); run with OSB_TEST_CLAUDE=1 locally.
func TestCLIProviderReal(t *testing.T) {
	if os.Getenv("OSB_TEST_CLAUDE") == "" {
		t.Skip("set OSB_TEST_CLAUDE=1 to run against the real claude CLI")
	}
	resp, err := NewCLIProvider("").Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: `You are a test bot. Reply ONLY with the exact JSON: {"answer":"pong"}`},
		{Role: RoleUser, Content: "ping"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "pong") {
		t.Fatalf("real claude did not follow the system prompt; got %q", resp.Text)
	}
}

// fakeRunner captures the RunSpec and returns a canned CLI JSON envelope, so the sandbox wiring can be
// tested (mounts, network, stdin, cmd) without Docker.
type fakeRunner struct{ got runner.RunSpec }

func (f *fakeRunner) Name() string { return "fake" }
func (f *fakeRunner) Run(_ context.Context, spec runner.RunSpec) (runner.Result, error) {
	f.got = spec
	return runner.Result{ExitCode: 0, Stdout: []byte(`{"is_error":false,"result":"SANDBOXED","usage":{"input_tokens":1,"output_tokens":2}}`)}, nil
}

func TestCLIProviderSandboxIsolatesCredential(t *testing.T) {
	fr := &fakeRunner{}
	p := NewCLIProvider("claude")
	p.Sandbox = &CLISandbox{Runner: fr, Image: "osb/claude:latest", CredentialSrc: "/home/u/.claude/.credentials.json"}

	resp, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: "SYS"},
		{Role: RoleUser, Content: "hello there"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "SANDBOXED" || resp.InputTokens != 1 || resp.OutputTokens != 2 {
		t.Fatalf("parsed sandbox response = %+v", resp)
	}

	spec := fr.got
	if spec.Image != "osb/claude:latest" {
		t.Fatalf("image = %q", spec.Image)
	}
	// The sandbox must mount EXACTLY the credential file, read-only, and nothing else.
	if len(spec.Mounts) != 1 {
		t.Fatalf("sandbox must mount exactly one path, got %+v", spec.Mounts)
	}
	m := spec.Mounts[0]
	if m.Source != "/home/u/.claude/.credentials.json" || !m.ReadOnly || m.Target != "/root/.claude/.credentials.json" {
		t.Fatalf("credential mount wrong / not read-only: %+v", m)
	}
	// The CLI is invoked with the system prompt as a flag and the conversation on stdin.
	joined := strings.Join(spec.Cmd, " ")
	if len(spec.Cmd) == 0 || spec.Cmd[0] != "claude" || !strings.Contains(joined, "--append-system-prompt SYS") || !strings.Contains(joined, "--output-format json") {
		t.Fatalf("cmd wrong: %v", spec.Cmd)
	}
	if !strings.Contains(string(spec.Stdin), "hello there") || strings.Contains(string(spec.Stdin), "SYS") {
		t.Fatalf("stdin wrong: %q", spec.Stdin)
	}
	// The sandbox needs egress to reach the API — it must NOT be network-isolated.
	if spec.Network != "bridge" {
		t.Fatalf("egress network = %q, want the bridge default", spec.Network)
	}
	foundHome := false
	for _, e := range spec.Env {
		if e == "HOME=/root" {
			foundHome = true
		}
	}
	if !foundHome {
		t.Fatalf("HOME not set for the container: %v", spec.Env)
	}
}

func TestNewCLISandboxConfig(t *testing.T) {
	// Sandbox on but no image → a clear error.
	if _, err := New(Config{Type: "claude-cli", CLISandbox: true}); err == nil {
		t.Fatal("sandbox without an image should error")
	}
	// Sandbox on with an image and explicit credential.
	p, err := New(Config{Type: "claude-cli", CLISandbox: true, CLIImage: "img", CLICredential: "/c/cred.json"})
	if err != nil {
		t.Fatal(err)
	}
	cp := p.(*CLIProvider)
	if cp.Sandbox == nil || cp.Sandbox.Image != "img" || cp.Sandbox.CredentialSrc != "/c/cred.json" {
		t.Fatalf("sandbox not configured: %+v", cp.Sandbox)
	}
	// Default claude-cli is NOT sandboxed.
	p2, _ := New(Config{Type: "claude-cli"})
	if p2.(*CLIProvider).Sandbox != nil {
		t.Fatal("default claude-cli must run on the host, not sandboxed")
	}
}

func TestCLIProviderSurfacesError(t *testing.T) {
	bin, _, _ := fakeClaude(t, `{"is_error":true,"result":"quota exceeded"}`)
	if _, err := NewCLIProvider(bin).Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("expected the CLI error to surface, got %v", err)
	}
}
