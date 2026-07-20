package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// DiscoveredModel is one model a connection reports it can serve, before enrichment (ADR-0052). The raw
// backend rarely returns pricing/context/tags — that metadata comes from the curated overlay, keyed by
// family. DisplayName/Family are best-effort ("" when the backend doesn't say).
type DiscoveredModel struct {
	ID          string
	DisplayName string
	Family      string
}

// ModelLister is a provider that can enumerate the models it serves. Implemented per connection type
// (OpenAI-compatible, Anthropic, and — later — the Bedrock/Foundry gateways). A provider without this
// capability degrades to the curated overlay plus any operator-pinned custom ids.
type ModelLister interface {
	ListModels(ctx context.Context) ([]DiscoveredModel, error)
}

// ListModels discovers a provider's models if it supports discovery. The bool reports whether discovery
// was even attempted (false for a provider with no ModelLister, e.g. the CLI/mock backends), so callers
// can distinguish "no lister" from "lister returned nothing".
func ListModels(ctx context.Context, p Provider) ([]DiscoveredModel, bool, error) {
	// Unwrap decorators (e.g. the DLP guard) to reach the real backend.
	for {
		if u, ok := p.(interface{ Unwrap() Provider }); ok {
			p = u.Unwrap()
			continue
		}
		break
	}
	lister, ok := p.(ModelLister)
	if !ok {
		return nil, false, nil
	}
	out, err := lister.ListModels(ctx)
	return out, true, err
}

// ListModels enumerates models via the OpenAI-compatible GET {BaseURL}/models endpoint. This covers
// OpenAI, DeepSeek, xAI Grok, and Ollama's OpenAI-compat shim — they differ only by base URL and key.
func (p *OpenAIProvider) ListModels(ctx context.Context) ([]DiscoveredModel, error) {
	if p.BaseURL == "" {
		return nil, fmt.Errorf("llm %s: base URL not set", p.Name())
	}
	endpoint := strings.TrimRight(p.BaseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	p.setAuth(req)
	body, err := doDiscovery(p.HTTP, req, p.Name())
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("llm %s: decode models: %w", p.Name(), err)
	}
	models := make([]DiscoveredModel, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, DiscoveredModel{ID: m.ID})
	}
	return models, nil
}

// ListModels enumerates models via the Anthropic GET {BaseURL}/v1/models endpoint, which (unlike the
// OpenAI shape) returns a human display_name.
func (a *AnthropicProvider) ListModels(ctx context.Context) ([]DiscoveredModel, error) {
	if a.APIKey == "" {
		return nil, fmt.Errorf("llm anthropic: API key not set")
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	endpoint := strings.TrimRight(base, "/") + "/v1/models?limit=1000"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	body, err := doDiscovery(a.HTTP, req, a.Name())
	if err != nil {
		return nil, err
	}
	var out struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("llm anthropic: decode models: %w", err)
	}
	models := make([]DiscoveredModel, 0, len(out.Data))
	for _, m := range out.Data {
		if m.ID == "" {
			continue
		}
		models = append(models, DiscoveredModel{ID: m.ID, DisplayName: m.DisplayName})
	}
	return models, nil
}

// doDiscovery runs a discovery GET and returns the body, mapping non-2xx to an error (best-effort — the
// caller falls back to the overlay).
func doDiscovery(client *http.Client, req *http.Request, name string) ([]byte, error) {
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("llm %s: %s: %s", name, resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}
