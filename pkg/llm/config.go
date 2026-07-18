package llm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Config selects and configures a provider.
type Config struct {
	Type    string // mock | claude-cli | anthropic | ollama | deepseek | grok | openai | azure
	BaseURL string
	Model   string
	APIKey  string
	Bin     string // for claude-cli
	// NativeTools uses native tool-use (ADR-0017) for anthropic/openai-compatible providers instead
	// of the prompted text protocol. On by default in the config paths (FromEnv / the provider
	// registry); the prompted fallback still covers backends without native tool support.
	NativeTools bool
	// CLISandbox runs claude-cli inside a runner container mounting only the credential file (ADR-0018).
	// Off by default (direct host exec). CLIImage is required when on.
	CLISandbox    bool
	CLIImage      string // container image with the `claude` CLI installed
	CLICredential string // host credential path (default ~/.claude/.credentials.json)
	CLINetwork    string // egress network (default "bridge")
}

// New builds a provider from a Config, applying sensible per-type defaults.
func New(cfg Config) (Provider, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "", "mock":
		return &MockProvider{}, nil
	case "claude-cli", "cli", "claude":
		p := NewCLIProvider(cfg.Bin)
		if cfg.CLISandbox {
			if cfg.CLIImage == "" {
				return nil, fmt.Errorf("llm: claude-cli sandbox requires an image (OSB_LLM_CLI_IMAGE)")
			}
			cred := cfg.CLICredential
			if cred == "" {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, fmt.Errorf("llm: cannot locate ~/.claude/.credentials.json: %w", err)
				}
				cred = filepath.Join(home, ".claude", ".credentials.json")
			}
			p.Sandbox = &CLISandbox{
				Runner:        runner.LocalRunner{},
				Image:         cfg.CLIImage,
				CredentialSrc: cred,
				Network:       cfg.CLINetwork,
			}
		}
		return p, nil
	case "anthropic":
		return &AnthropicProvider{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, Model: orDefault(cfg.Model, "claude-sonnet-5"), UseNativeTools: cfg.NativeTools}, nil
	case "ollama":
		return openAICompat("ollama", orDefault(cfg.BaseURL, "http://127.0.0.1:11434/v1"), cfg.APIKey, cfg.Model, cfg.NativeTools), nil
	case "deepseek":
		return openAICompat("deepseek", orDefault(cfg.BaseURL, "https://api.deepseek.com/v1"), cfg.APIKey, orDefault(cfg.Model, "deepseek-chat"), cfg.NativeTools), nil
	case "grok", "xai":
		return openAICompat("grok", orDefault(cfg.BaseURL, "https://api.x.ai/v1"), cfg.APIKey, orDefault(cfg.Model, "grok-2-latest"), cfg.NativeTools), nil
	case "openai", "azure", "openai-compat":
		if cfg.BaseURL == "" {
			return nil, fmt.Errorf("llm: %s requires a base URL", cfg.Type)
		}
		return openAICompat(cfg.Type, cfg.BaseURL, cfg.APIKey, cfg.Model, cfg.NativeTools), nil
	default:
		return nil, fmt.Errorf("llm: unknown provider type %q", cfg.Type)
	}
}

// FromEnv builds a provider from OSB_LLM_* environment variables. This is a development
// convenience; in production, API keys come from the encrypted vault, never the environment.
// An unset OSB_LLM_PROVIDER yields the MockProvider.
func FromEnv() (Provider, error) {
	return New(Config{
		Type:          os.Getenv("OSB_LLM_PROVIDER"),
		BaseURL:       os.Getenv("OSB_LLM_BASE_URL"),
		Model:         os.Getenv("OSB_LLM_MODEL"),
		APIKey:        os.Getenv("OSB_LLM_API_KEY"),
		Bin:           os.Getenv("OSB_LLM_BIN"),
		NativeTools:   os.Getenv("OSB_LLM_NATIVE_TOOLS") != "0", // native by default; set 0 to force prompted
		CLISandbox:    os.Getenv("OSB_LLM_CLI_SANDBOX") == "1",
		CLIImage:      os.Getenv("OSB_LLM_CLI_IMAGE"),
		CLICredential: os.Getenv("OSB_LLM_CLI_CREDENTIAL"),
		CLINetwork:    os.Getenv("OSB_LLM_CLI_NETWORK"),
	})
}

func openAICompat(label, baseURL, apiKey, model string, nativeTools bool) *OpenAIProvider {
	return &OpenAIProvider{Label: label, BaseURL: baseURL, APIKey: apiKey, Model: model, UseNativeTools: nativeTools}
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
	// Unwrap decorators (e.g. the DLP guard) so classification reflects the real backend.
	if u, ok := p.(interface{ Unwrap() Provider }); ok {
		return IsLocal(u.Unwrap())
	}
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
