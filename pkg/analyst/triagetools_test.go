package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// list_observations exposes the routing attributes so the agent can triage by reachability/exposure, and is
// project-scoped (ADR-0035).
func TestListObservationsToolExposesAttributes(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	other, _ := db.CreateProject(ctx, store.NewProject{Name: "Other"})

	mine, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &projectID, Origin: model.OriginTool, Title: "SQLi", Severity: "high", RuleID: "taint.sql",
		Location: "a.py:42", Attributes: map[string]string{"reachable": "true", "exposed_route": "POST /q", "route_observed": "true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := db.CreateObservation(ctx, model.Observation{
		ProjectID: &other.ID, Origin: model.OriginTool, Title: "other", Severity: "low",
	})

	exec := Executor(ExecDeps{Store: db, ProjectID: projectID})
	out, err := exec(ctx, agent.ToolCall{Tool: "list_observations", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mine.ID) || strings.Contains(out, theirs.ID) {
		t.Fatalf("list_observations leaked across projects: %s", out)
	}
	// The routing attributes must be present for the agent to prioritize on them.
	for _, want := range []string{"reachable", "exposed_route", "POST /q", "route_observed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("list_observations dropped attribute %q: %s", want, out)
		}
	}
}

func TestListObservationsUnreviewedOnly(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	un, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &projectID, Origin: model.OriginTool, Title: "unrev", Severity: "high"})
	conf, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &projectID, Origin: model.OriginTool, Title: "conf", Severity: "high"})
	if err := db.ReviewObservation(ctx, conf.ID, model.ReviewConfirmed); err != nil {
		t.Fatal(err)
	}
	exec := Executor(ExecDeps{Store: db, ProjectID: projectID})
	out, _ := exec(ctx, agent.ToolCall{Tool: "list_observations", Args: map[string]any{"unreviewed_only": true}})
	if !strings.Contains(out, un.ID) || strings.Contains(out, conf.ID) {
		t.Fatalf("unreviewed_only filter wrong: %s", out)
	}
}

func TestListInvestigationsTool(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &projectID, Origin: model.OriginTool, Title: "vuln", Severity: "high"})
	inv, err := db.CreateInvestigation(ctx, model.Investigation{ProjectID: projectID, ObservationID: obs.ID, Title: "vuln"})
	if err != nil {
		t.Fatal(err)
	}
	exec := Executor(ExecDeps{Store: db, ProjectID: projectID})
	out, err := exec(ctx, agent.ToolCall{Tool: "list_investigations", Args: map[string]any{"open_only": true}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, inv.ID) || !strings.Contains(out, obs.ID) {
		t.Fatalf("list_investigations missing the open investigation: %s", out)
	}
}

// The autonomous assessment must never let the agent confirm findings — every step profile lacks
// create_finding (ADR-0035, "propose, human confirms").
func TestAssessmentPlaybookNeverConfirmsFindings(t *testing.T) {
	var assessment *Playbook
	for i := range builtinPlaybooks {
		if builtinPlaybooks[i].ID == "assessment" {
			assessment = &builtinPlaybooks[i]
		}
	}
	if assessment == nil {
		t.Fatal("assessment playbook not found")
	}
	// DAG sanity: each dependency references an earlier step key.
	seen := map[string]bool{}
	for _, s := range assessment.Steps {
		for _, dep := range s.DependsOn {
			if !seen[dep] {
				t.Fatalf("step %q depends on %q which is not an earlier step", s.Key, dep)
			}
		}
		seen[s.Key] = true
	}
	for _, s := range assessment.Steps {
		if s.Gate {
			continue // a gate is a human-approval pause, not a delegated step (no profile)
		}
		p, ok := builtinProfile(s.Profile)
		if !ok {
			t.Fatalf("step %q uses unknown profile %q", s.Key, s.Profile)
		}
		for _, tool := range p.Tools {
			if tool == "create_finding" {
				t.Fatalf("step %q (profile %q) can create_finding — the autonomous run must only propose", s.Key, s.Profile)
			}
		}
	}
}
