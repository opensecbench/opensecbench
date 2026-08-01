package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func TestCLIProviderPassesModel(t *testing.T) {
	bin, argsFile, _ := fakeClaude(t, `{"is_error":false,"result":"OK"}`)

	// A per-request model (e.g. from tag routing) is passed as --model.
	p := NewCLIProvider(bin)
	if _, err := p.Complete(context.Background(), CompletionRequest{
		Model:    "claude-opus-4-8",
		Messages: []Message{{Role: RoleUser, Content: "hi"}},
	}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--model claude-opus-4-8") {
		t.Fatalf("per-request model not passed; args = %q", args)
	}

	// The connection default model is used when the request names none.
	p2 := NewCLIProvider(bin)
	p2.Model = "claude-haiku-4-5"
	if _, err := p2.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(args), "--model claude-haiku-4-5") {
		t.Fatalf("connection default model not passed; args = %q", args)
	}

	// No model configured → no --model flag (CLI uses its own default).
	p3 := NewCLIProvider(bin)
	if _, err := p3.Complete(context.Background(), CompletionRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatal(err)
	}
	args, _ = os.ReadFile(argsFile)
	if strings.Contains(string(args), "--model") {
		t.Fatalf("--model should be absent when no model set; args = %q", args)
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

// The claude-cli connection type now builds a NATIVE Anthropic provider authenticated by the subscription
// OAuth token (not a `claude -p` subprocess), so it can run tool-using agents (native tools everywhere).
func TestClaudeSubscriptionIsNative(t *testing.T) {
	p, err := New(Config{Type: "claude-cli", CLICredential: "/c/cred.json", NativeTools: true})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := p.(*AnthropicProvider)
	if !ok {
		t.Fatalf("claude-cli should build an AnthropicProvider, got %T", p)
	}
	if a.CredentialFile != "/c/cred.json" || !a.NativeTools() || a.Model != "claude-sonnet-5" {
		t.Fatalf("subscription provider misconfigured: %+v", a)
	}
	if a.Name() != "claude-subscription" {
		t.Fatalf("name = %q", a.Name())
	}
}

func TestReadSubscriptionToken(t *testing.T) {
	dir := t.TempDir()
	// Valid, unexpired token.
	good := filepath.Join(dir, "good.json")
	_ = os.WriteFile(good, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-x","expiresAt":`+itoa64(time.Now().Add(time.Hour).UnixMilli())+`}}`), 0o600)
	if tok, err := readSubscriptionToken(good); err != nil || tok != "sk-ant-oat01-x" {
		t.Fatalf("read = %q err=%v", tok, err)
	}
	// Expired token → a clear error.
	exp := filepath.Join(dir, "exp.json")
	_ = os.WriteFile(exp, []byte(`{"claudeAiOauth":{"accessToken":"x","expiresAt":1}}`), 0o600)
	if _, err := readSubscriptionToken(exp); err == nil {
		t.Fatal("expired token should error")
	}
	// Missing file → error.
	if _, err := readSubscriptionToken(filepath.Join(dir, "nope.json")); err == nil {
		t.Fatal("missing credential file should error")
	}
}

func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// TestCLIProviderSandboxReal exercises the full sandbox path — CLIProvider.Sandbox + the real
// LocalRunner + the osb/claude-cli image + your ambient credential — end to end. It makes a real API
// call, so it's skipped by default. Build the image first (`make claude-image`), then run with
// OSB_TEST_CLAUDE_SANDBOX=1.
func TestCLIProviderSandboxReal(t *testing.T) {
	if os.Getenv("OSB_TEST_CLAUDE_SANDBOX") == "" {
		t.Skip("set OSB_TEST_CLAUDE_SANDBOX=1 (and `make claude-image`) to run the sandboxed CLI end to end")
	}
	p, err := New(Config{Type: "claude-cli", CLISandbox: true}) // default image + ~/.claude/.credentials.json
	if err != nil {
		t.Fatal(err)
	}
	resp, err := p.Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: `You are a test bot. Reply ONLY with the exact JSON: {"answer":"pong"}`},
		{Role: RoleUser, Content: "ping"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Text, "pong") {
		t.Fatalf("sandboxed claude did not follow the system prompt; got %q", resp.Text)
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

func TestSubscriptionRequestShape(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "c.json")
	_ = os.WriteFile(cred, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-tok","expiresAt":`+strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)+`}}`), 0o600)

	// Subscription (OAuth) path: Claude Code client shape — Bearer + oauth beta + CLI User-Agent + x-app.
	a := &AnthropicProvider{CredentialFile: cred}
	req, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err := a.setAuth(req); err != nil {
		t.Fatal(err)
	}
	if req.Header.Get("Authorization") != "Bearer sk-ant-oat01-tok" {
		t.Fatalf("authorization = %q", req.Header.Get("Authorization"))
	}
	if req.Header.Get("anthropic-beta") != "oauth-2025-04-20" {
		t.Fatalf("oauth beta missing: %q", req.Header.Get("anthropic-beta"))
	}
	if !strings.HasPrefix(req.Header.Get("User-Agent"), "claude-cli/") || !strings.Contains(req.Header.Get("User-Agent"), "(external, cli)") {
		t.Fatalf("user-agent not the Claude Code shape: %q", req.Header.Get("User-Agent"))
	}
	if req.Header.Get("x-app") != "cli" {
		t.Fatalf("x-app = %q", req.Header.Get("x-app"))
	}
	if req.Header.Get("x-api-key") != "" {
		t.Fatal("OAuth path must not send x-api-key")
	}

	// API-key path: x-api-key, no Bearer, no CLI user-agent.
	k := &AnthropicProvider{APIKey: "sk-ant-api03-key"}
	kreq, _ := http.NewRequest(http.MethodPost, "https://api.anthropic.com/v1/messages", nil)
	if err := k.setAuth(kreq); err != nil {
		t.Fatal(err)
	}
	if kreq.Header.Get("x-api-key") != "sk-ant-api03-key" || kreq.Header.Get("Authorization") != "" {
		t.Fatalf("api-key path headers wrong: x-api-key=%q auth=%q", kreq.Header.Get("x-api-key"), kreq.Header.Get("Authorization"))
	}
}

func TestSubscriptionBillingBlockAndHeaders(t *testing.T) {
	dir := t.TempDir()
	cred := filepath.Join(dir, "c.json")
	_ = os.WriteFile(cred, []byte(`{"claudeAiOauth":{"accessToken":"sk-ant-oat01-tok","expiresAt":`+strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10)+`}}`), 0o600)

	var gotBody []byte
	var gotUA, gotAuth, gotApp string
	a := &AnthropicProvider{
		CredentialFile: cred, UseNativeTools: true,
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotBody, _ = io.ReadAll(r.Body)
			gotUA, gotAuth, gotApp = r.Header.Get("User-Agent"), r.Header.Get("Authorization"), r.Header.Get("x-app")
			return jsonResp(`{"model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
		})},
	}
	if _, err := a.Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: "You are the Analyst."},
		{Role: RoleUser, Content: "hi"},
	}}); err != nil {
		t.Fatal(err)
	}

	// Headers: Claude Code client shape.
	if gotAuth != "Bearer sk-ant-oat01-tok" || gotApp != "cli" || !strings.HasPrefix(gotUA, "claude-cli/") {
		t.Fatalf("headers wrong: ua=%q auth=%q app=%q", gotUA, gotAuth, gotApp)
	}

	// Body: system is an array whose FIRST block is the billing prefix, then the real persona.
	var body struct {
		System []struct {
			Text string `json:"text"`
		} `json:"system"`
	}
	if err := jsonUnmarshalStrict(gotBody, &body); err != nil {
		t.Fatalf("decode body: %v — %s", err, gotBody)
	}
	if len(body.System) < 2 {
		t.Fatalf("system should have billing block + persona, got %d: %s", len(body.System), gotBody)
	}
	if !strings.Contains(body.System[0].Text, "Claude Code") {
		t.Fatalf("first system block is not the billing prefix: %q", body.System[0].Text)
	}
	if !strings.Contains(body.System[1].Text, "You are the Analyst") {
		t.Fatalf("persona not preserved as the second block: %q", body.System[1].Text)
	}
}

func jsonUnmarshalStrict(b []byte, v any) error { return json.Unmarshal(b, v) }

// TestCacheBreakpoints verifies the request carries prompt-cache breakpoints on the system prefix and on
// the trailing messages, so the agent loop's resent history is served from cache instead of re-billed.
func TestCacheBreakpoints(t *testing.T) {
	var gotBody []byte
	a := &AnthropicProvider{
		Model: "claude-sonnet-5", UseNativeTools: true, APIKey: "k",
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotBody, _ = io.ReadAll(r.Body)
			return jsonResp(`{"model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1,"output_tokens":1}}`), nil
		})},
	}
	// A multi-turn conversation, as the loop resends it: user, assistant tool_use, tool_result, user.
	if _, err := a.Complete(context.Background(), CompletionRequest{Messages: []Message{
		{Role: RoleSystem, Content: "You are the Analyst."},
		{Role: RoleUser, Content: "one"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Tool: "list_kb", Args: map[string]any{}}}},
		{Role: RoleTool, ToolCallID: "call_1", Content: "big result"},
		{Role: RoleUser, Content: "two"},
	}}); err != nil {
		t.Fatal(err)
	}

	var body struct {
		System []struct {
			CacheControl map[string]any `json:"cache_control"`
		} `json:"system"`
		Messages []struct {
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := jsonUnmarshalStrict(gotBody, &body); err != nil {
		t.Fatalf("decode body: %v — %s", err, gotBody)
	}
	// System prefix (tools cache with it, ahead in cache order): breakpoint on the final block.
	if n := len(body.System); n == 0 || body.System[n-1].CacheControl["type"] != "ephemeral" {
		t.Fatalf("system prefix not cache-marked: %s", gotBody)
	}
	// The last two messages carry a cache breakpoint on their final content block; earlier ones don't.
	cached := func(raw json.RawMessage) bool {
		var blocks []map[string]any
		if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
			return false
		}
		_, ok := blocks[len(blocks)-1]["cache_control"]
		return ok
	}
	msgs := body.Messages
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d: %s", len(msgs), gotBody)
	}
	if !cached(msgs[3].Content) || !cached(msgs[2].Content) {
		t.Fatalf("last two messages should be cache-marked: %s", gotBody)
	}
	if cached(msgs[0].Content) || cached(msgs[1].Content) {
		t.Fatalf("earlier messages should not be cache-marked: %s", gotBody)
	}
}

// TestBilledInputFold checks that cache-aware usage folds into one base-input-equivalent: fresh at 1x,
// writes at 1.25x, reads at 0.1x — so the recorded count reflects what's billed.
func TestBilledInputFold(t *testing.T) {
	cases := []struct {
		fresh, create, read, want int
	}{
		{100, 0, 0, 100},       // caching off → unchanged
		{0, 0, 1000, 100},      // pure cache read: 1000 × 0.1
		{0, 800, 0, 1000},      // pure cache write: 800 × 1.25
		{200, 800, 1000, 1300}, // 200 + 1000 + 100
	}
	for _, c := range cases {
		u := anthropicUsage{InputTokens: c.fresh, CacheCreationInputTokens: c.create, CacheReadInputTokens: c.read}
		if got := u.billedInput(); got != c.want {
			t.Errorf("billedInput(fresh=%d create=%d read=%d) = %d, want %d", c.fresh, c.create, c.read, got, c.want)
		}
	}
}
