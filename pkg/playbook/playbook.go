// Package playbook holds reusable tactics: named sequences of capability steps run against an
// asset (ADR-0003). Built-ins ship here; third-party playbooks load as extension packages later.
// v1 runs steps sequentially; a dependency graph is a future refinement.
package playbook

// Step is one capability invocation in a playbook.
type Step struct {
	Capability string         `json:"capability"`
	Params     map[string]any `json:"params,omitempty"`
}

// Playbook is a named tactic.
type Playbook struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
}

var builtins = []Playbook{
	{
		ID:          "recon",
		Name:        "Source recon",
		Description: "Inventory the source tree.",
		Steps:       []Step{{Capability: "source-inventory"}},
	},
	{
		ID:          "sast",
		Name:        "Static analysis",
		Description: "Run Semgrep static analysis.",
		Steps:       []Step{{Capability: "semgrep"}},
	},
	{
		ID:          "source-review",
		Name:        "Source review",
		Description: "Inventory the source, then run Semgrep static analysis.",
		Steps: []Step{
			{Capability: "source-inventory"},
			{Capability: "semgrep"},
		},
	},
	{
		ID:          "web-recon",
		Name:        "Web recon",
		Description: "Probe a live web service for basic fingerprinting.",
		Steps:       []Step{{Capability: "http-probe"}},
	},
}

// BuiltIns returns the built-in playbooks.
func BuiltIns() []Playbook { return builtins }

// Get returns a playbook by id.
func Get(id string) (Playbook, bool) {
	for _, p := range builtins {
		if p.ID == id {
			return p, true
		}
	}
	return Playbook{}, false
}
