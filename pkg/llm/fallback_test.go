package llm

import (
	"context"
	"errors"
	"testing"
)

// stubProvider returns a fixed response or error, and records whether it was called.
type stubProvider struct {
	name   string
	resp   string
	err    error
	native bool
	called bool
}

func (s *stubProvider) Name() string        { return s.name }
func (s *stubProvider) NativeTools() bool    { return s.native }
func (s *stubProvider) Complete(_ context.Context, req CompletionRequest) (CompletionResponse, error) {
	s.called = true
	if s.err != nil {
		return CompletionResponse{}, s.err
	}
	return CompletionResponse{Text: s.resp, Model: req.Model}, nil
}

func TestFallbackServesTopWhenHealthy(t *testing.T) {
	top := &stubProvider{name: "sub", resp: "from-sub"}
	next := &stubProvider{name: "gw", resp: "from-gw"}
	f := &FallbackProvider{Entries: []FallbackEntry{{top, "haiku"}, {next, "bedrock-haiku"}}}

	resp, err := f.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "from-sub" || resp.Model != "haiku" {
		t.Fatalf("expected top to serve, got %+v", resp)
	}
	if next.called {
		t.Fatal("fallback should not be called when the top succeeds")
	}
}

func TestFallbackOnTransientError(t *testing.T) {
	top := &stubProvider{name: "sub", err: errors.New("llm cli: 429 Too Many Requests: rate limit")}
	next := &stubProvider{name: "gw", resp: "from-gw"}
	f := &FallbackProvider{Entries: []FallbackEntry{{top, "haiku"}, {next, "bedrock-haiku"}}}

	resp, err := f.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !top.called || !next.called {
		t.Fatal("both should be tried on a transient top failure")
	}
	if resp.Text != "from-gw" || resp.Model != "bedrock-haiku" {
		t.Fatalf("should have fallen through to the gateway, got %+v", resp)
	}
}

func TestFallbackDoesNotRetryTerminalError(t *testing.T) {
	// A 400 / content refusal would be rejected identically by the next provider — don't waste the call.
	top := &stubProvider{name: "sub", err: errors.New("llm: 400 invalid_request_error: bad tool schema")}
	next := &stubProvider{name: "gw", resp: "from-gw"}
	f := &FallbackProvider{Entries: []FallbackEntry{{top, "a"}, {next, "b"}}}

	if _, err := f.Complete(context.Background(), CompletionRequest{}); err == nil {
		t.Fatal("a terminal error should be returned, not swallowed")
	}
	if next.called {
		t.Fatal("must not fall through on a terminal (non-transient) error")
	}
}

func TestFallbackSkipsMismatchedToolMode(t *testing.T) {
	// The chain renders in the first entry's mode; an entry with a different mode is skipped.
	top := &stubProvider{name: "native", err: errors.New("connection refused"), native: true}
	prompted := &stubProvider{name: "prompted", resp: "x", native: false}
	backup := &stubProvider{name: "native2", resp: "from-native2", native: true}
	f := &FallbackProvider{Entries: []FallbackEntry{{top, "a"}, {prompted, "b"}, {backup, "c"}}}

	resp, err := f.Complete(context.Background(), CompletionRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if prompted.called {
		t.Fatal("a mismatched-tool-mode entry must be skipped")
	}
	if resp.Text != "from-native2" {
		t.Fatalf("should serve the next same-mode entry, got %+v", resp)
	}
}
