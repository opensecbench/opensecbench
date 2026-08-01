// Package action defines custom, user-authored operations that run against a single finding or
// observation (ADR-0059). An action is one of two kinds — an LLM agent (delegated to a saved profile)
// or a sandboxed script (a templated RunSpec) — templated from the subject's fields, with its output
// attached back to the subject as evidence. The set is environment-specific, so the platform ships
// editable examples (BuiltIns) and lets operators author their own.
//
// This package is deliberately execution-free: it holds the record, the applicability predicate, the
// templating, and script planning. The executor lives in pkg/analyst, where the delegate loop and the
// sandbox runner already are.
package action

import (
	"strings"
	"time"

	"github.com/opensecbench/opensecbench/pkg/runner"
)

// Kind is how an action executes.
type Kind string

const (
	KindAgent  Kind = "agent"  // delegate to a saved profile with a templated instruction
	KindScript Kind = "script" // run a templated command in the sandbox
)

// Subject kinds an action can attach to.
const (
	SubjectFinding     = "finding"
	SubjectObservation = "observation"
)

// Action is a saved, reusable operation an operator runs against one subject.
type Action struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Kind        Kind   `json:"kind"`
	// SubjectKinds are the surfaces this action appears on: "finding", "observation", or both.
	SubjectKinds []string  `json:"subject_kinds"`
	AppliesWhen  Predicate `json:"applies_when"`
	// Technique tags the action with a rules-of-engagement technique (ADR-0051/0054): an action that reads
	// telemetry, sends traffic, or changes state declares one and is blocked unless the engagement permits
	// it. Empty = passive, always allowed.
	Technique string `json:"technique,omitempty"`

	// Agent kind.
	ProfileID   string `json:"profile_id,omitempty"`
	Instruction string `json:"instruction,omitempty"`

	// Script kind (the ContainerCapability shape, subject-templated).
	Image          string   `json:"image,omitempty"`
	Cmd            []string `json:"cmd,omitempty"`
	Network        string   `json:"network,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MemoryMB       int      `json:"memory_mb,omitempty"`
	CPUs           float64  `json:"cpus,omitempty"`

	Output OutputSpec `json:"output"`

	Builtin   bool      `json:"builtin,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
}

// OutputSpec declares where a run's result goes beyond the always-on evidence attachment (ADR-0059).
type OutputSpec struct {
	// RecordObservations lets an agent action's profile record observations (its toolset already has
	// create_observation), so a hit becomes a tracked observation rather than a text blob.
	RecordObservations bool `json:"record_observations,omitempty"`
	// WriteToPath, if set, writes the produced artifact to this path under the project workspace, so a
	// generated rule lands where the operator keeps them (P2 target; empty = evidence only).
	WriteToPath string `json:"write_to_path,omitempty"`
}

// Predicate filters which subjects show an action. An empty predicate matches everything.
type Predicate struct {
	MinSeverity string   `json:"min_severity,omitempty"` // e.g. "high" — matches this severity and above
	Statuses    []string `json:"statuses,omitempty"`     // subject status/review-state must be one of these
	CWEPrefixes []string `json:"cwe_prefixes,omitempty"` // subject CWE must start with one of these (e.g. "CWE-89")
}

// Subject is the normalized view of a finding or observation an action templates against and its
// applicability is tested on. The caller (API layer) builds it from the loaded finding/observation.
type Subject struct {
	Kind        string `json:"kind"` // "finding" | "observation"
	ID          string `json:"id"`
	Title       string `json:"title"`
	Severity    string `json:"severity"`
	Status      string `json:"status"`
	CWE         string `json:"cwe,omitempty"`
	Description string `json:"description,omitempty"`
	Location    string `json:"location,omitempty"`
	Environment string `json:"environment,omitempty"` // the project engagement's environment
}

// sevRank orders severities so MinSeverity can mean "this and above". Unknown severities rank lowest so
// a predicate never hides a subject on a typo.
var sevRank = map[string]int{"critical": 5, "high": 4, "medium": 3, "low": 2, "info": 1}

// AppliesTo reports whether the action should be offered for the given subject.
func (a Action) AppliesTo(s Subject) bool {
	if !contains(a.SubjectKinds, s.Kind) {
		return false
	}
	return a.AppliesWhen.matches(s)
}

func (p Predicate) matches(s Subject) bool {
	if p.MinSeverity != "" && sevRank[strings.ToLower(s.Severity)] < sevRank[strings.ToLower(p.MinSeverity)] {
		return false
	}
	if len(p.Statuses) > 0 && !contains(p.Statuses, s.Status) {
		return false
	}
	if len(p.CWEPrefixes) > 0 {
		ok := false
		for _, pre := range p.CWEPrefixes {
			if strings.HasPrefix(s.CWE, pre) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// tokens are the {{...}} substitutions available to both kinds.
func (s Subject) tokens() map[string]string {
	return map[string]string{
		"subject.kind":        s.Kind,
		"subject.id":          s.ID,
		"subject.title":       s.Title,
		"subject.severity":    s.Severity,
		"subject.status":      s.Status,
		"subject.cwe":         s.CWE,
		"subject.description": s.Description,
		"subject.location":    s.Location,
		"project.environment": s.Environment,
	}
}

// env exposes the subject as environment variables so a `sh -c` script can reference them safely (values
// are passed by the runtime, never shell-interpolated — a title cannot inject a command).
func (s Subject) env() []string {
	t := s.tokens()
	keys := []string{"subject.kind", "subject.id", "subject.title", "subject.severity", "subject.status",
		"subject.cwe", "subject.description", "subject.location", "project.environment"}
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		name := "OSB_" + strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(k, ".", "_"), "-", "_"))
		out = append(out, name+"="+t[k])
	}
	return out
}

// Render substitutes {{token}} placeholders in a template string (used for the agent instruction).
func Render(tmpl string, s Subject) string {
	out := tmpl
	for k, v := range s.tokens() {
		out = strings.ReplaceAll(out, "{{"+k+"}}", v)
	}
	return out
}

// PlanScript builds the sandboxed RunSpec for a script action. Subject tokens are substituted into argv
// tokens (exec argv, not a shell) and also exposed as OSB_SUBJECT_* env vars. The project workspace is
// mounted read-write at /work so a script can write a produced artifact there.
func (a Action) PlanScript(s Subject, workDir string) runner.RunSpec {
	subs := s.tokens()
	cmd := make([]string, len(a.Cmd))
	for i, tok := range a.Cmd {
		out := tok
		for k, v := range subs {
			out = strings.ReplaceAll(out, "{{"+k+"}}", v)
		}
		cmd[i] = out
	}
	spec := runner.RunSpec{
		Image:    a.Image,
		Cmd:      cmd,
		Env:      s.env(),
		Network:  orDefault(a.Network, "none"),
		Workdir:  orDefault("", "/work"),
		MemoryMB: orInt(a.MemoryMB, 512),
		CPUs:     orFloat(a.CPUs, 1),
		Timeout:  orTimeout(a.TimeoutSeconds, 2*time.Minute),
	}
	if workDir != "" {
		spec.Mounts = []runner.Mount{{Source: workDir, Target: "/work", ReadOnly: false}}
	}
	return spec
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
func orInt(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}
func orFloat(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
func orTimeout(sec int, def time.Duration) time.Duration {
	if sec > 0 {
		return time.Duration(sec) * time.Second
	}
	return def
}
