package action

import (
	"strings"
	"testing"
)

func TestAppliesTo(t *testing.T) {
	a := Action{
		SubjectKinds: []string{SubjectFinding},
		AppliesWhen:  Predicate{MinSeverity: "high", Statuses: []string{"open"}, CWEPrefixes: []string{"CWE-89"}},
	}
	cases := []struct {
		name string
		s    Subject
		want bool
	}{
		{"match", Subject{Kind: "finding", Severity: "critical", Status: "open", CWE: "CWE-89"}, true},
		{"wrong kind", Subject{Kind: "observation", Severity: "critical", Status: "open", CWE: "CWE-89"}, false},
		{"too low sev", Subject{Kind: "finding", Severity: "low", Status: "open", CWE: "CWE-89"}, false},
		{"wrong status", Subject{Kind: "finding", Severity: "high", Status: "accepted", CWE: "CWE-89"}, false},
		{"wrong cwe", Subject{Kind: "finding", Severity: "high", Status: "open", CWE: "CWE-79"}, false},
	}
	for _, c := range cases {
		if got := a.AppliesTo(c.s); got != c.want {
			t.Errorf("%s: AppliesTo = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestEmptyPredicateMatchesEverything(t *testing.T) {
	a := Action{SubjectKinds: []string{SubjectFinding, SubjectObservation}}
	if !a.AppliesTo(Subject{Kind: "observation", Severity: "info", Status: "new"}) {
		t.Error("empty predicate should match any subject of a declared kind")
	}
}

func TestRender(t *testing.T) {
	s := Subject{Title: "SQLi", Location: "a.go:1", Severity: "high", Environment: "prod"}
	got := Render("Check {{subject.title}} at {{subject.location}} in {{project.environment}}", s)
	want := "Check SQLi at a.go:1 in prod"
	if got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

func TestPlanScriptSubstitutesAndIsInjectionSafe(t *testing.T) {
	// A malicious title must land as one argv token / env value, never as shell syntax.
	s := Subject{Kind: "finding", ID: "f1", Title: "x; rm -rf /", Location: "a.go:1"}
	a := Action{
		Kind:  KindScript,
		Image: "alpine:3",
		Cmd:   []string{"sh", "-c", "grep {{subject.title}} /logs"},
	}
	spec := a.PlanScript(s, "/tmp/work")
	if spec.Cmd[2] != "grep x; rm -rf / /logs" {
		t.Errorf("token substituted into argv token, got %q", spec.Cmd[2])
	}
	// The whole grep command is a single argv token to `sh -c`, so the substituted title is data, not a
	// second shell command. And the safe path is the env var, passed by the runtime.
	var titleEnv string
	for _, e := range spec.Env {
		if strings.HasPrefix(e, "OSB_SUBJECT_TITLE=") {
			titleEnv = e
		}
	}
	if titleEnv != "OSB_SUBJECT_TITLE=x; rm -rf /" {
		t.Errorf("subject title env = %q", titleEnv)
	}
	if spec.Mounts[0].Source != "/tmp/work" || spec.Mounts[0].ReadOnly {
		t.Errorf("workspace should mount read-write at /work, got %+v", spec.Mounts)
	}
}

func TestBuiltInsWellFormed(t *testing.T) {
	for _, a := range BuiltIns() {
		if a.ID == "" || a.Name == "" || a.Kind == "" || len(a.SubjectKinds) == 0 {
			t.Errorf("built-in %q missing required fields", a.ID)
		}
		if a.Kind == KindAgent && (a.ProfileID == "" || a.Instruction == "") {
			t.Errorf("built-in agent %q needs a profile and instruction", a.ID)
		}
		if _, ok := Get(a.ID); !ok {
			t.Errorf("Get(%q) not found", a.ID)
		}
	}
}
