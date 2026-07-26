package analyst

// Governance is consequence-tier, not actor-based (ADR-0054, building on ADR-0019). The gate keys on what an
// action's worst consequence is — can it be undone? does it leave the host? — NOT on "is it an agent." So a
// human and the agent follow the same rule: capability parity (ADR-0053). Two layers sit on top: an autonomy
// envelope the human grants (the control surface), and per-(tool[,profile]) trust-curve overrides. The scope
// guard and DLP floor are separate and enforced regardless — relaxing a gate removes a prompt, never a wall.

// Decision is the outcome of the policy for one tool call.
type Decision string

const (
	DecisionAuto    Decision = "auto"    // run without prompting
	DecisionApprove Decision = "approve" // pause for human approval
)

// Consequence classifies an action by the worst thing running it can do — what the decision can't take back.
type Consequence string

const (
	Reversible  Consequence = "reversible"  // internal, undoable state (create/edit/disposition) — runs free
	External    Consequence = "external"    // leaves the host: outbound traffic / fetch — can't un-send
	Execute     Consequence = "execute"     // runs code / scanners / a sub-agent — real side effects + cost
	Destructive Consequence = "destructive" // irreversible data loss — always confirms (safety floor)
)

// toolConsequence classifies the actions whose consequence is above Reversible. Everything else — the reads
// and the reversible writes (create_finding, set_coverage, set_finding_status, triage_observation, KB drafts,
// workspace writes, show, …) — is Reversible and runs freely for agent and human alike. This map, not an
// "is it an agent" flag, is what the gate consults.
var toolConsequence = map[string]Consequence{
	"send_request":   External, // leaves the host (scope-guarded regardless); you can't un-send a request
	"web_fetch":      External, // outbound fetch (a preapproved research source bypasses the gate earlier)
	"run_code":       Execute,  // runs a command in a sandbox
	"run_capability": Execute,  // runs a scanner against an asset
	"run_playbook":   Execute,  // runs a sequence of capabilities
	"delegate":       Execute,  // spawns a sub-agent that runs work and widens the toolset
}

// consequenceOf returns a tool's class (Reversible when unlisted).
func consequenceOf(tool string) Consequence {
	if c, ok := toolConsequence[tool]; ok {
		return c
	}
	return Reversible
}

// Autonomy is the human's consent envelope (ADR-0054, the control surface): the highest consequence tier that
// runs without a confirm. Default is Cautious. Destructive always confirms regardless.
type Autonomy string

const (
	AutonomyCautious Autonomy = "cautious" // only Reversible runs free; External/Execute confirm  (default)
	AutonomyTrusted  Autonomy = "trusted"  // Reversible + External + Execute run free; Destructive confirms
)

// confirms reports whether a consequence tier pauses for approval under this envelope.
func (a Autonomy) confirms(c Consequence) bool {
	switch c {
	case Reversible, "":
		return false // reversible actions never pre-confirm — oversight is undo/audit, after the fact
	case Destructive:
		return true // safety floor
	default: // External, Execute
		return a != AutonomyTrusted
	}
}

// Rule overrides the default decision for a tool, optionally scoped to one agent profile. An empty Profile
// matches any profile. This is the fine-grained trust-curve tuner on top of the tier defaults.
type Rule struct {
	Tool     string   `json:"tool"`
	Profile  string   `json:"profile,omitempty"`
	Decision Decision `json:"decision"`
}

// Policy resolves whether a tool call needs human approval, from the consequence-tier base (under the current
// autonomy envelope) plus any override rules.
type Policy struct {
	rules    []Rule
	autonomy Autonomy
}

// NewPolicy builds a policy from override rules at the default (Cautious) envelope.
func NewPolicy(rules []Rule) Policy { return Policy{rules: rules, autonomy: AutonomyCautious} }

// DefaultPolicy is the conservative base: Cautious envelope, no overrides.
func DefaultPolicy() Policy { return Policy{autonomy: AutonomyCautious} }

// WithAutonomy returns a copy at the given consent envelope.
func (p Policy) WithAutonomy(a Autonomy) Policy {
	if a == "" {
		a = AutonomyCautious
	}
	p.autonomy = a
	return p
}

// Autonomy returns the policy's consent envelope.
func (p Policy) Autonomy() Autonomy {
	if p.autonomy == "" {
		return AutonomyCautious
	}
	return p.autonomy
}

// Rules returns the policy's override rules.
func (p Policy) Rules() []Rule { return p.rules }

// Decide returns the decision for a tool call under this policy and the acting profile. The base is the
// consequence tier under the autonomy envelope; the most specific matching rule wins — a profile-scoped rule
// beats a tool-only rule.
func (p Policy) Decide(tool, profile string) Decision {
	dec := DecisionAuto
	if p.Autonomy().confirms(consequenceOf(tool)) {
		dec = DecisionApprove
	}
	best := -1
	for _, r := range p.rules {
		if r.Tool != tool {
			continue
		}
		if r.Decision != DecisionAuto && r.Decision != DecisionApprove {
			continue
		}
		spec := 0
		if r.Profile != "" {
			if r.Profile != profile {
				continue
			}
			spec = 1
		}
		if spec > best {
			best = spec
			dec = r.Decision
		}
	}
	return dec
}

// NeedsApproval reports whether the call must pause for a human under this policy and profile.
func (p Policy) NeedsApproval(tool, profile string) bool {
	return p.Decide(tool, profile) == DecisionApprove
}

// SensitiveTools lists the tools that confirm by default (i.e. are above Reversible) — for display / policy
// editing. Reversible tools are absent because they run freely.
func SensitiveTools() []string {
	out := make([]string, 0, len(toolConsequence))
	for t := range toolConsequence {
		out = append(out, t)
	}
	return out
}

// ToolConsequences returns each classified tool's consequence tier, so the UI can show why a tool confirms.
func ToolConsequences() map[string]string {
	out := make(map[string]string, len(toolConsequence))
	for t, c := range toolConsequence {
		out[t] = string(c)
	}
	return out
}
