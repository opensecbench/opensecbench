// Package catalog is the curated model catalog (ADR-0021). Provider APIs don't expose a reliable model
// list, so this embedded, editable dataset is the source of truth for the model picker and tag defaults.
// Users may still configure a model id not listed here.
package catalog

import (
	_ "embed"
	"encoding/json"
	"strings"
)

//go:embed models.json
var raw []byte

// Model is one catalog entry: which provider serves it, its id/name, context window, per-million-token
// pricing (0 for local), and default routing tags.
type Model struct {
	Provider      string   `json:"provider"`
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	Family        string   `json:"family"`
	ContextWindow int      `json:"context_window"`
	InputPerMTok  float64  `json:"input_per_mtok"`
	OutputPerMTok float64  `json:"output_per_mtok"`
	DefaultTags   []string `json:"default_tags"`
}

var models []Model

func init() {
	if err := json.Unmarshal(raw, &models); err != nil {
		panic("catalog: models.json is invalid: " + err.Error())
	}
}

// Models returns the full curated catalog.
func Models() []Model { return models }

// ByProvider returns the catalog models served by a provider key (e.g. "anthropic", "openai", "ollama").
func ByProvider(provider string) []Model {
	var out []Model
	for _, m := range models {
		if m.Provider == provider {
			out = append(out, m)
		}
	}
	return out
}

// Get returns a catalog model by (provider, id).
func Get(provider, id string) (Model, bool) {
	for _, m := range models {
		if m.Provider == provider && m.ID == id {
			return m, true
		}
	}
	return Model{}, false
}

// MetaForFamily returns representative overlay metadata (context/price/tags) for a model family — the
// first catalog entry with that family. This is the connection-independent enrichment source (ADR-0052):
// a family reachable via several connections (Anthropic direct vs Bedrock) shares one metadata record.
func MetaForFamily(family string) (Model, bool) {
	if family == "" {
		return Model{}, false
	}
	for _, m := range models {
		if m.Family == family {
			return m, true
		}
	}
	return Model{}, false
}

// Family normalizes a served model id to a catalog family so discovered models can borrow overlay
// metadata. It first tries an exact (provider, id) catalog hit, then strips any gateway provider prefix
// (Bedrock's "anthropic.claude-..." / "meta.llama-...") and matches the id against known family tokens.
// Returns "" when nothing matches — the model still lists, just without enriched price/context/tags.
func Family(provider, id string) string {
	if m, ok := Get(provider, id); ok && m.Family != "" {
		return m.Family
	}
	s := strings.ToLower(id)
	// A gateway id like "anthropic.claude-sonnet-4-5-...-v1:0" carries "<vendor>." before the real id.
	if i := strings.IndexByte(s, '.'); i > 0 {
		if _, known := familyVendors[s[:i]]; known {
			s = s[i+1:]
		}
	}
	for _, tok := range familyTokens {
		if strings.Contains(s, tok.token) {
			return tok.family
		}
	}
	return ""
}

// familyVendors are the Bedrock/Foundry gateway id prefixes we strip before family matching.
var familyVendors = map[string]bool{
	"anthropic": true, "meta": true, "mistral": true, "deepseek": true,
	"amazon": true, "cohere": true, "ai21": true, "openai": true, "us": true, "eu": true,
}

// familyTokens map a substring of a served id to its catalog family. Order matters — more specific
// tokens first (e.g. "gpt-5" before a hypothetical "gpt").
var familyTokens = []struct{ token, family string }{
	{"opus", "opus"},
	{"sonnet", "sonnet"},
	{"haiku", "haiku"},
	{"fable", "fable"},
	{"deepseek", "deepseek"},
	{"grok", "grok"},
	{"gpt-5", "gpt-5"},
	{"gpt", "gpt-5"},
	{"llama", "llama"},
	{"qwen", "qwen"},
	{"mistral", "mistral"},
}
