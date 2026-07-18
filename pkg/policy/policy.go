// Package policy defines governance profiles (ADR-0006, the plan): a named posture bundling agent
// data-access and LLM provider-routing rules. A profile is selected to switch how the Analyst may
// use private data and which providers may see it.
package policy

// Profile bundles the governance rules that switch per posture.
type Profile struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// AllowExternalForPrivate: may capability output / content for a PRIVATE asset be sent to an
	// external (non-local) LLM provider? false = block (corporate/strict).
	AllowExternalForPrivate bool `json:"allow_external_for_private"`
	// AgentSeesPrivate: may the Analyst read private-tagged data into context at all?
	AgentSeesPrivate bool `json:"agent_sees_private"`
}

// Built-in profiles (the plan's personal / corporate / strict postures).
var builtIns = map[string]Profile{
	"personal": {
		Name: "personal", Description: "Solo/personal use: private data may go to any configured provider.",
		AllowExternalForPrivate: true, AgentSeesPrivate: true,
	},
	"corporate": {
		Name: "corporate", Description: "Corporate default: private data never leaves to a public provider; the agent may still read it locally.",
		AllowExternalForPrivate: false, AgentSeesPrivate: true,
	},
	"strict": {
		Name: "strict", Description: "Strict: private data never reaches an external provider and is withheld from the agent entirely.",
		AllowExternalForPrivate: false, AgentSeesPrivate: false,
	},
}

// Default is the conservative default profile name.
const Default = "corporate"

// Get returns a profile by name, falling back to the default.
func Get(name string) Profile {
	if p, ok := builtIns[name]; ok {
		return p
	}
	return builtIns[Default]
}

// All returns the built-in profiles.
func All() []Profile {
	return []Profile{builtIns["personal"], builtIns["corporate"], builtIns["strict"]}
}
