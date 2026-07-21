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
	// AllowExternalForInternal: may an INTERNAL asset (the middle tier — ours, not public, but not
	// confidential) be sent to an external provider? personal + corporate allow it; strict blocks it.
	// open_source is always allowed; there is no flag for it.
	AllowExternalForInternal bool `json:"allow_external_for_internal"`
	// AgentSeesPrivate: may the Analyst read private-tagged data into context at all?
	AgentSeesPrivate bool `json:"agent_sees_private"`
}

// Built-in profiles (the plan's personal / corporate / strict postures). Egress ceiling by tier:
// personal → private, corporate → internal, strict → open_source only.
var builtIns = map[string]Profile{
	"personal": {
		Name: "personal", Description: "Solo/personal use (default): any asset may go to any configured provider.",
		AllowExternalForPrivate: true, AllowExternalForInternal: true, AgentSeesPrivate: true,
	},
	"corporate": {
		Name: "corporate", Description: "Corporate: private data never leaves to an external provider; internal data may. The agent still reads everything locally.",
		AllowExternalForPrivate: false, AllowExternalForInternal: true, AgentSeesPrivate: true,
	},
	"strict": {
		Name: "strict", Description: "Strict: nothing but open-source reaches an external provider, and private data is withheld from the agent entirely.",
		AllowExternalForPrivate: false, AllowExternalForInternal: false, AgentSeesPrivate: false,
	},
}

// Default is the default profile name. Personal suits the solo/local operator this tool is built for;
// teams handling third-party data switch to corporate or strict.
const Default = "personal"

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
