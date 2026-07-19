// Package catalog is the curated model catalog (ADR-0021). Provider APIs don't expose a reliable model
// list, so this embedded, editable dataset is the source of truth for the model picker and tag defaults.
// Users may still configure a model id not listed here.
package catalog

import (
	_ "embed"
	"encoding/json"
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
