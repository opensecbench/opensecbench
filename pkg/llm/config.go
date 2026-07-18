package llm

import (
	"fmt"
	"os"
	"strings"
)

// Config selects and configures a provider.
type Config struct {
	Type    string // mock | claude-cli | anthropic | ollama | deepseek | grok | openai | azure
	BaseURL string
	Model   string
	APIKey  string
	Bin     string // for claude-cli
}

// New builds a provider from a Config, applying sensible per-type defaults.
func New(cfg Config) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "mock":
		return &MockProvider{}, nil
	case "claude-cli", "cli", "claude":
		return NewCLIProvider(cfg.Bin), nil
	case "anthropic":
		return &AnthropicProvider{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: orDefault(cfg.Model, "claude-sonnet-5")}, nil
	case "ollama":
		return openAICompat("ollama", orDefault(cfg.BaseURL, "http://127.0.0.1:11434/v1"), cfg.APIKey, cfg.Model), nil
	case "deepseek":
		return openAICompat("deepseek", orDefault(cfg.BaseURL, "https://api.deepseek.com/v1"), cfg.APIKey, orDefault(cfg.Model, "deepseek-chat")), nil
	case "grok", "xai":
		return openAICompat("grok", orDefault(cfg.BaseURL, "https://api.x.ai/v1"), cfg.APIKey, orDefault(cfg.Model, "grok-2-latest")), nil
	case "openai", "azure", "openai-compat":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm: %s requires a base URL", cfg.Type)
		}
		return openAICompat(cfg.Type, cfg.BaseURL, cfg.APIKey, cfg.Model), nil
	default:
		return nil, fmt.Errorf("llm: unknown provider type %q", cfg.Type)
	}
}

// FromEnv builds a provider from OSB_LLM_* environment variables. This is a development
// convenience; in production, API keys come from the encrypted vault, never the environment.
// An unset OSB_LLM_PROVIDER yields the MockProvider.
func FromEnv() (Provider, error) {
	return New(Config{
		Type:    os.Getenv("OSB_LLM_PROVIDER"),
		BaseURL: os.Getenv("OSB_LLM_BASE_URL"),
		Model:   os.Getenv("OSB_LLM_MODEL"),
		APIKey:  os.Getenv("OSB_LLM_API_KEY"),
		Bin:     os.Getenv("OSB_LLM_BIN"),
	})
}

func openAICompat(label, baseURL, apiKey, model string) *OpenAIProvider {
	return &OpenAIProvider{Label: label, BaseURL: baseURL, APIKey: apiKey, Model: model}
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

// IsLocal reports whether a provider keeps data on the local machine / private network (so it is
// safe for private data under a strict egress policy). Mock and a loopback Ollama are local;
// hosted APIs and the claude CLI (which calls the Anthropic API) are not.
func IsLocal(p Provider) bool {
	switch v := p.(type) {
	case *MockProvider:
		return true
	case *OpenAIProvider:
		return v.Label == "ollama" || isLoopbackURL(v.BaseURL)
	default:
		return false
	}
}

func isLoopbackURL(u string) bool {
	u = strings.ToLower(u)
	return strings.Contains(u, "127.0.0.1") || strings.Contains(u, "localhost") || strings.Contains(u, "://[::1]")
}
