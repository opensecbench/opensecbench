package llm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
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

func TestCLIProviderSurfacesError(t *testing.T) {
	bin, _, _ := fakeClaude(t, `{"is_error":true,"result":"quota exceeded"}`)
	if _, err := NewCLIProvider(bin).Complete(context.Background(), CompletionRequest{
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err == nil || !strings.Contains(err.Error(), "quota exceeded") {
		t.Fatalf("expected the CLI error to surface, got %v", err)
	}
}
