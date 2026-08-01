package action

// BuiltIns are the shipped example actions (ADR-0059). They exist to be cloned and tailored to an
// environment: point the log hunt at your SIEM, the rule generators at your house format. All three are
// agent kind and passive (no declared technique) so they run out of the box; an operator sets a technique
// once an action reaches production telemetry or state, and the engagement gate then applies.
func BuiltIns() []Action {
	return []Action{
		{
			ID:           "hunt-logs-for-abuse",
			Name:         "Hunt logs for abuse",
			Description:  "Search available logs and telemetry for real exploitation attempts of this issue.",
			Icon:         "🔎",
			Kind:         KindAgent,
			SubjectKinds: []string{SubjectFinding, SubjectObservation},
			ProfileID:    "generalist",
			Instruction: "Search our available logs and telemetry for real exploitation attempts of " +
				"\"{{subject.title}}\" ({{subject.severity}}) at {{subject.location}}. Context: {{subject.description}}. " +
				"Report any hits with timestamps, source IPs, and request lines. If you find none, say so plainly. " +
				"This runs even before the issue is confirmed — the point is to learn whether anyone has already tried it.",
			Output: OutputSpec{RecordObservations: true},
		},
		{
			ID:           "generate-opengrep-rule",
			Name:         "Generate OpenGrep rule",
			Description:  "Author a static-analysis rule that would catch this issue's pattern.",
			Icon:         "📐",
			Kind:         KindAgent,
			SubjectKinds: []string{SubjectFinding, SubjectObservation},
			ProfileID:    "code-analysis",
			Instruction: "Author an OpenGrep/Semgrep rule that would detect \"{{subject.title}}\" at " +
				"{{subject.location}} ({{subject.cwe}}). Read the code around the location to identify the sink and " +
				"the tainted input, then write a precise rule (patterns + pattern-not to avoid false positives). " +
				"Output the rule as YAML and explain what it matches and what it deliberately does not.",
		},
		{
			ID:           "generate-waf-rule",
			Name:         "Generate WAF rule",
			Description:  "Draft a WAF / ModSecurity rule to detect or block this issue.",
			Icon:         "🛡️",
			Kind:         KindAgent,
			SubjectKinds: []string{SubjectFinding, SubjectObservation},
			ProfileID:    "code-analysis",
			Instruction: "Draft a WAF rule (ModSecurity/CRS style) to detect and block exploitation of " +
				"\"{{subject.title}}\" at {{subject.location}} ({{subject.cwe}}). Base the signature on the actual " +
				"request shape that reaches the vulnerable code. Note the false-positive risk and whether it should " +
				"start in detect-only mode.",
		},
	}
}

// Get returns a built-in action by id.
func Get(id string) (Action, bool) {
	for _, a := range BuiltIns() {
		if a.ID == id {
			return a, true
		}
	}
	return Action{}, false
}
