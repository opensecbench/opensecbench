package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// BedrockProvider talks to Amazon Bedrock — a gateway that serves many model families (Anthropic, Llama,
// Mistral, …) behind one credential (ADR-0052). Inference uses the Converse API; discovery uses the
// control-plane ListFoundationModels. Auth is AWS SigV4 (see sigv4.go). This adapter uses the prompted
// tool protocol (NativeTools=false): messages arrive pre-flattened to text, so Converse plain-text
// completion covers the loop; native Converse tool-use is a later refinement.
type BedrockProvider struct {
	Region string
	Creds  awsCreds
	Model  string
	HTTP   *http.Client
	// now is injectable for tests; nil uses time.Now.
	now func() time.Time
}

// Name identifies the provider.
func (b *BedrockProvider) Name() string { return "bedrock" }

// NativeTools reports false — Bedrock here uses the prompted text protocol (see type doc).
func (b *BedrockProvider) NativeTools() bool { return false }

func (b *BedrockProvider) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

func (b *BedrockProvider) client() *http.Client {
	if b.HTTP != nil {
		return b.HTTP
	}
	return http.DefaultClient
}

// Complete calls the Bedrock Converse API for a text completion.
func (b *BedrockProvider) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if b.Region == "" {
		return CompletionResponse{}, errors.New("llm bedrock: region not set")
	}
	model := req.Model
	if model == "" {
		model = b.Model
	}
	if model == "" {
		return CompletionResponse{}, errors.New("llm bedrock: model not set")
	}
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	// Translate canonical (pre-flattened) messages into Converse shape: system is a top-level list;
	// user/assistant turns carry a content list of {text} blocks.
	var system []map[string]any
	var messages []map[string]any
	for _, m := range req.Messages {
		if m.Role == RoleSystem {
			system = append(system, map[string]any{"text": m.Content})
			continue
		}
		role := m.Role
		if role != RoleAssistant {
			role = RoleUser // Bedrock accepts only user/assistant turns
		}
		messages = append(messages, map[string]any{
			"role":    role,
			"content": []map[string]any{{"text": m.Content}},
		})
	}
	payload := map[string]any{
		"messages":        messages,
		"inferenceConfig": map[string]any{"maxTokens": maxTokens},
	}
	if len(system) > 0 {
		payload["system"] = system
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	endpoint := fmt.Sprintf("https://bedrock-runtime.%s.amazonaws.com/model/%s/converse", b.Region, model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return CompletionResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	signV4(httpReq, body, "bedrock", b.Region, b.Creds, b.clock())

	resp, err := b.client().Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= http.StatusBadRequest {
		bb, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return CompletionResponse{}, fmt.Errorf("llm bedrock: %s: %s", resp.Status, strings.TrimSpace(string(bb)))
	}

	var out struct {
		Output struct {
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		} `json:"output"`
		Usage struct {
			InputTokens  int `json:"inputTokens"`
			OutputTokens int `json:"outputTokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return CompletionResponse{}, err
	}
	var text strings.Builder
	for _, c := range out.Output.Message.Content {
		text.WriteString(c.Text)
	}
	return CompletionResponse{
		Text:         text.String(),
		InputTokens:  out.Usage.InputTokens,
		OutputTokens: out.Usage.OutputTokens,
	}, nil
}

// ListModels enumerates Bedrock foundation models via the control-plane ListFoundationModels (ADR-0052).
// Model ids carry a family prefix (e.g. "anthropic.claude-...") which the catalog normalizer maps to a
// family for overlay enrichment.
func (b *BedrockProvider) ListModels(ctx context.Context) ([]DiscoveredModel, error) {
	if b.Region == "" {
		return nil, errors.New("llm bedrock: region not set")
	}
	endpoint := fmt.Sprintf("https://bedrock.%s.amazonaws.com/foundation-models", b.Region)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	signV4(req, nil, "bedrock", b.Region, b.Creds, b.clock())
	body, err := doDiscovery(b.client(), req, "bedrock")
	if err != nil {
		return nil, err
	}
	var out struct {
		ModelSummaries []struct {
			ModelID   string `json:"modelId"`
			ModelName string `json:"modelName"`
		} `json:"modelSummaries"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("llm bedrock: decode models: %w", err)
	}
	models := make([]DiscoveredModel, 0, len(out.ModelSummaries))
	for _, m := range out.ModelSummaries {
		if m.ModelID == "" {
			continue
		}
		models = append(models, DiscoveredModel{ID: m.ModelID, DisplayName: m.ModelName})
	}
	return models, nil
}

// parseBedrockCreds decodes the sealed credential blob for a Bedrock connection. Two forms are accepted:
// JSON {"access_key_id","secret_access_key","session_token"} or the shorthand
// "ACCESS_KEY_ID:SECRET_ACCESS_KEY[:SESSION_TOKEN]".
func parseBedrockCreds(sealed string) (awsCreds, error) {
	sealed = strings.TrimSpace(sealed)
	if sealed == "" {
		return awsCreds{}, errors.New("llm bedrock: credentials required (access key + secret)")
	}
	if strings.HasPrefix(sealed, "{") {
		var c struct {
			AccessKeyID     string `json:"access_key_id"`
			SecretAccessKey string `json:"secret_access_key"`
			SessionToken    string `json:"session_token"`
		}
		if err := json.Unmarshal([]byte(sealed), &c); err != nil {
			return awsCreds{}, fmt.Errorf("llm bedrock: bad credential JSON: %w", err)
		}
		if c.AccessKeyID == "" || c.SecretAccessKey == "" {
			return awsCreds{}, errors.New("llm bedrock: access_key_id and secret_access_key required")
		}
		return awsCreds{AccessKeyID: c.AccessKeyID, SecretAccessKey: c.SecretAccessKey, SessionToken: c.SessionToken}, nil
	}
	parts := strings.SplitN(sealed, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return awsCreds{}, errors.New("llm bedrock: credentials must be ACCESS_KEY_ID:SECRET_ACCESS_KEY[:SESSION_TOKEN] or JSON")
	}
	c := awsCreds{AccessKeyID: parts[0], SecretAccessKey: parts[1]}
	if len(parts) == 3 {
		c.SessionToken = parts[2]
	}
	return c, nil
}
