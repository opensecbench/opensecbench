package analyst

// Approval policy (ADR-0019 §5). Governance is a trust-curve policy, not a fixed gate: it starts
// conservative — every mutating or outbound tool asks for approval — and specific (tool [, profile])
// pairs are promoted to "auto" as they earn trust. This only decides whether a human is prompted; the
// scope guard and the DLP floor are separate and enforced regardless (relaxing approval removes a
// prompt, never a wall).

// Decision is the outcome of the policy for one tool call.
type Decision string

const (
	DecisionAuto    Decision = "auto"    // run without prompting
	DecisionApprove Decision = "approve" // pause for human approval
)

// sensitiveTools require approval by default (they mutate assessment state, send traffic, or execute
// code). Every other tool — the reads — defaults to auto.
var sensitiveTools = map[string]bool{
	"run_capability": true,
	"run_playbook":   true,
	"send_request":   true,
	"set_coverage":   true,
	"create_finding": true,
	"run_code":       true,
	"delegate":       true,
	"web_fetch":      true, // gated, but a preapproved source is auto-approved before this (ADR-0038)
}

// Rule overrides the default decision for a tool, optionally scoped to one agent profile. An empty
// Profile matches any profile.
type Rule struct {
	Tool     string   `json:"tool"`
	Profile  string   `json:"profile,omitempty"`
	Decision Decision `json:"decision"`
}

// Policy resolves whether a tool call needs human approval, from the conservative base plus any rules.
type Policy struct {
	rules []Rule
}

// NewPolicy builds a policy from override rules (invalid rules are ignored at Decide time).
func NewPolicy(rules []Rule) Policy { return Policy{rules: rules} }

// DefaultPolicy is fully conservative: no overrides, so every sensitive tool asks for approval.
func DefaultPolicy() Policy { return Policy{} }

// Rules returns the policy's override rules.
func (p Policy) Rules() []Rule { return p.rules }

// Decide returns the decision for a tool call under this policy and the acting profile. The base is
// approve for a sensitive tool, otherwise auto; the most specific matching rule wins — a profile-scoped
// rule beats a tool-only rule.
func (p Policy) Decide(tool, profile string) Decision {
	dec := DecisionAuto
	if sensitiveTools[tool] {
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

// SensitiveTools lists the tools that require approval by default (for display / policy editing).
func SensitiveTools() []string {
	out := make([]string, 0, len(sensitiveTools))
	for t := range sensitiveTools {
		out = append(out, t)
	}
	return out
}
