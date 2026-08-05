package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/credentials"
)

// roundTripFunc lets a test intercept the outbound request and return a canned response.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func jsonResp(body string) *http.Response {
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{},
	}
}

func TestBedrockComplete(t *testing.T) {
	var captured *http.Request
	var capturedBody []byte
	b := &BedrockProvider{
		Region: "us-east-1",
		Creds:  credentials.NewStaticCredentialsProvider("AKID", "secret", ""),
		Model:  "anthropic.claude-sonnet-4-5-v1:0",
		now:    func() time.Time { return time.Unix(0, 0) },
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			captured = r
			capturedBody, _ = io.ReadAll(r.Body)
			return jsonResp(`{"output":{"message":{"content":[{"text":"hi there"}]}},"usage":{"inputTokens":11,"outputTokens":3}}`), nil
		})},
	}

	resp, err := b.Complete(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: RoleSystem, Content: "be brief"},
			{Role: RoleUser, Content: "hello"},
		},
		MaxTokens: 128,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "hi there" || resp.InputTokens != 11 || resp.OutputTokens != 3 {
		t.Fatalf("unexpected response: %+v", resp)
	}

	// Endpoint + SigV4 header shape.
	if !strings.Contains(captured.URL.String(), "bedrock-runtime.us-east-1.amazonaws.com/model/anthropic.claude-sonnet-4-5-v1:0/converse") {
		t.Errorf("bad endpoint: %s", captured.URL)
	}
	if !strings.HasPrefix(captured.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=AKID/") {
		t.Errorf("missing SigV4 auth: %q", captured.Header.Get("Authorization"))
	}

	// Converse body shape: system top-level, messages carry {role, content:[{text}]}.
	var body struct {
		System   []map[string]any `json:"system"`
		Messages []struct {
			Role    string           `json:"role"`
			Content []map[string]any `json:"content"`
		} `json:"messages"`
		InferenceConfig map[string]any `json:"inferenceConfig"`
	}
	if err := json.Unmarshal(capturedBody, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.System) != 1 || body.System[0]["text"] != "be brief" {
		t.Errorf("system not mapped: %+v", body.System)
	}
	if len(body.Messages) != 1 || body.Messages[0].Role != "user" || body.Messages[0].Content[0]["text"] != "hello" {
		t.Errorf("messages not mapped: %+v", body.Messages)
	}
	if body.InferenceConfig["maxTokens"].(float64) != 128 {
		t.Errorf("maxTokens not passed: %+v", body.InferenceConfig)
	}
}

func TestBedrockListModels(t *testing.T) {
	b := &BedrockProvider{
		Region: "us-east-1",
		Creds:  credentials.NewStaticCredentialsProvider("AKID", "secret", ""),
		now:    func() time.Time { return time.Unix(0, 0) },
		HTTP: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if !strings.Contains(r.URL.String(), "bedrock.us-east-1.amazonaws.com/foundation-models") {
				t.Errorf("bad discovery endpoint: %s", r.URL)
			}
			return jsonResp(`{"modelSummaries":[{"modelId":"anthropic.claude-opus-4-5-v1:0","modelName":"Claude Opus 4.5"},{"modelId":"meta.llama3-70b","modelName":"Llama 3 70B"}]}`), nil
		})},
	}
	got, err := b.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "anthropic.claude-opus-4-5-v1:0" || got[0].DisplayName != "Claude Opus 4.5" {
		t.Fatalf("unexpected models: %+v", got)
	}
}

func TestParseBedrockCreds(t *testing.T) {
	// JSON form.
	c, err := parseBedrockCreds(`{"access_key_id":"AK","secret_access_key":"SK","session_token":"TK"}`)
	if err != nil || c.AccessKeyID != "AK" || c.SecretAccessKey != "SK" || c.SessionToken != "TK" {
		t.Fatalf("json creds: %+v err=%v", c, err)
	}
	// Shorthand with session token.
	c, err = parseBedrockCreds("AK:SK:TK")
	if err != nil || c.AccessKeyID != "AK" || c.SessionToken != "TK" {
		t.Fatalf("shorthand creds: %+v err=%v", c, err)
	}
	// Errors.
	if _, err := parseBedrockCreds(""); err == nil {
		t.Error("empty creds should error")
	}
	if _, err := parseBedrockCreds("AKonly"); err == nil {
		t.Error("missing secret should error")
	}
}

func TestBedrockInvocationID(t *testing.T) {
	cases := []struct {
		name     string
		id       string
		region   string
		infTypes []string
		want     string
	}{
		{"on-demand keeps bare id", "anthropic.claude-3-haiku", "us-east-1", []string{"ON_DEMAND"}, "anthropic.claude-3-haiku"},
		{"both keeps bare id", "anthropic.claude-3-5-sonnet", "us-east-1", []string{"ON_DEMAND", "INFERENCE_PROFILE"}, "anthropic.claude-3-5-sonnet"},
		{"profile-only gets us prefix", "anthropic.claude-sonnet-4-6", "us-east-1", []string{"INFERENCE_PROFILE"}, "us.anthropic.claude-sonnet-4-6"},
		{"profile-only eu region", "anthropic.claude-sonnet-4-6", "eu-west-1", []string{"INFERENCE_PROFILE"}, "eu.anthropic.claude-sonnet-4-6"},
		{"profile-only apac region", "anthropic.claude-sonnet-4-6", "ap-southeast-2", []string{"INFERENCE_PROFILE"}, "apac.anthropic.claude-sonnet-4-6"},
		{"gov region", "anthropic.claude-sonnet-4-6", "us-gov-west-1", []string{"INFERENCE_PROFILE"}, "us-gov.anthropic.claude-sonnet-4-6"},
		{"already prefixed untouched", "us.anthropic.claude-sonnet-4-6", "us-east-1", []string{"INFERENCE_PROFILE"}, "us.anthropic.claude-sonnet-4-6"},
		{"unknown region left bare", "anthropic.claude-sonnet-4-6", "ca-central-1", []string{"INFERENCE_PROFILE"}, "anthropic.claude-sonnet-4-6"},
		{"unknown inference types dont guess", "anthropic.claude-sonnet-4-6", "us-east-1", nil, "anthropic.claude-sonnet-4-6"},
	}
	for _, c := range cases {
		if got := bedrockInvocationID(c.id, c.region, c.infTypes); got != c.want {
			t.Errorf("%s: bedrockInvocationID(%q,%q,%v) = %q, want %q", c.name, c.id, c.region, c.infTypes, got, c.want)
		}
	}
}

func TestNewBedrockCredentials(t *testing.T) {
	// Explicit static key → a provider that retrieves exactly those credentials.
	p, err := newBedrockCredentials("AK:SK:TK", "us-east-1")
	if err != nil {
		t.Fatalf("static: %v", err)
	}
	got, err := p.Retrieve(context.Background())
	if err != nil {
		t.Fatalf("retrieve static: %v", err)
	}
	if got.AccessKeyID != "AK" || got.SecretAccessKey != "SK" || got.SessionToken != "TK" {
		t.Fatalf("static creds not resolved: %+v", got)
	}

	// Blank key → the AWS default chain: a non-nil provider is returned without error (actual credential
	// retrieval is deferred and environment-dependent, so it isn't exercised here).
	p, err = newBedrockCredentials("", "us-east-1")
	if err != nil || p == nil {
		t.Fatalf("default chain: provider=%v err=%v", p, err)
	}

	// A named profile that doesn't exist surfaces a clear error rather than silently falling back.
	if _, err := newBedrockCredentials("profile:no-such-profile-xyz", "us-east-1"); err == nil {
		t.Error("unknown profile should error")
	}
}
