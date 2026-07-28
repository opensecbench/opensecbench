package dlp

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/llm"
)

func TestInspectFindsSecretsCanariesPatterns(t *testing.T) {
	s := New(
		map[string]string{"TOKEN-abc-123": "api_token"},
		map[string]string{"OSB-CANARY-deadbeef": "planted-1"},
	)
	// Intentional DLP test fixture — the token/canary/AWS-key/JWT below are fakes we expect Inspect to
	// catch, not real secrets. The trailing marker tells gitleaks to skip this line (it must be inline).
	text := "here is TOKEN-abc-123 and OSB-CANARY-deadbeef plus AKIA1234567890ABCDEF and a jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkw.abcDEFghijk" //gitleaks:allow
	hits := s.Inspect(text)

	kinds := map[string]string{}
	for _, h := range hits {
		kinds[h.Kind] = h.Action
	}
	if kinds[KindSecret] != ActionBlock {
		t.Fatalf("secret should block: %+v", hits)
	}
	if kinds[KindCanary] != ActionBlock {
		t.Fatalf("canary should block: %+v", hits)
	}
	if kinds[KindPattern] != ActionAlert {
		t.Fatalf("pattern should alert: %+v", hits)
	}
}

func TestInspectClean(t *testing.T) {
	if hits := New(nil, nil).Inspect("nothing sensitive here"); len(hits) != 0 {
		t.Fatalf("clean text produced hits: %+v", hits)
	}
}

type fakeProvider struct{ called bool }

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Complete(context.Context, llm.CompletionRequest) (llm.CompletionResponse, error) {
	f.called = true
	return llm.CompletionResponse{Text: "ok"}, nil
}

func req(content string) llm.CompletionRequest {
	return llm.CompletionRequest{Messages: []llm.Message{{Role: "user", Content: content}}}
}

func TestGuardBlocksSecretOnExternal(t *testing.T) {
	inner := &fakeProvider{}
	var events []Hit
	load := func(context.Context) (map[string]string, map[string]string) {
		return map[string]string{"SEEKRIT": "prod_key"}, nil
	}
	g := Guard(inner, true, load, func(_ context.Context, h Hit, _ bool) { events = append(events, h) })

	_, err := g.Complete(context.Background(), req("please use SEEKRIT to log in"))
	if err == nil || !strings.Contains(err.Error(), "DLP") {
		t.Fatalf("expected DLP block error, got %v", err)
	}
	if inner.called {
		t.Fatal("inner provider should NOT be called when blocked")
	}
	if len(events) != 1 || events[0].Kind != KindSecret {
		t.Fatalf("expected a recorded secret hit: %+v", events)
	}
}

func TestGuardAllowsOnLocalButStillRecords(t *testing.T) {
	inner := &fakeProvider{}
	var blockedFlags []bool
	load := func(context.Context) (map[string]string, map[string]string) {
		return map[string]string{"SEEKRIT": "prod_key"}, nil
	}
	// external=false → local provider: secret content is allowed through (data stays on the box).
	g := Guard(inner, false, load, func(_ context.Context, _ Hit, blocked bool) { blockedFlags = append(blockedFlags, blocked) })

	if _, err := g.Complete(context.Background(), req("use SEEKRIT")); err != nil {
		t.Fatalf("local provider should not block: %v", err)
	}
	if !inner.called {
		t.Fatal("inner provider should be called on local egress")
	}
	if len(blockedFlags) != 1 || blockedFlags[0] {
		t.Fatalf("hit should be recorded as not-blocked on local: %+v", blockedFlags)
	}
}

func TestGuardUnwrapForClassifier(t *testing.T) {
	inner := &llm.MockProvider{Responses: []string{"hi"}}
	g := Guard(inner, false, nil, nil)
	if !llm.IsLocal(g) {
		t.Fatal("IsLocal should see through the guard to the local mock")
	}
}
