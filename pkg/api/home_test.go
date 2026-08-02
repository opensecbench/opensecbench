package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestHomeCockpit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/home")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var home struct {
		Approvals []any `json:"approvals"`
		Active    struct {
			Tasks   []any `json:"tasks"`
			Threads []any `json:"threads"`
		} `json:"active"`
		Projects  []any `json:"projects"`
		Schedules []any `json:"schedules"`
		Usage     struct {
			AllInput  int   `json:"all_input"`
			TopModels []any `json:"top_models"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&home); err != nil {
		t.Fatal(err)
	}
	// A fresh store: the cockpit responds with present (empty, not null) sections.
	if home.Approvals == nil || home.Active.Threads == nil || home.Active.Tasks == nil || home.Schedules == nil {
		t.Fatal("cockpit sections should be present (empty arrays), not null")
	}
	// Usage is present with zeroed totals on a fresh store (no runs recorded).
	if home.Usage.AllInput != 0 {
		t.Fatalf("fresh store should report zero all-time input tokens, got %d", home.Usage.AllInput)
	}
}

// TestHomeReviewBacklog verifies the cockpit surfaces per-project review work — an open finding,
// an unreviewed observation, and an open investigation — so "Waiting on you" can't read empty while
// work is queued.
func TestHomeReviewBacklog(t *testing.T) {
	srv, db := serverWithDB(t)
	ctx := t.Context()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Backlog"})
	app, _ := db.CreateApplication(ctx, proj.ID, "App")
	pid := proj.ID

	// An open finding (default status is "open") awaiting a disposition decision. Its supporting
	// observation must be confirmed before a finding can be created.
	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "SQLi", Severity: "high"})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)
	if _, err := db.CreateFinding(ctx, store.NewFinding{ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high", ObservationIDs: []string{obs.ID}}); err != nil {
		t.Fatalf("create finding: %v", err)
	}
	// An unreviewed observation awaiting triage.
	_, _ = db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "open port", Severity: "low"})
	// An open investigation.
	inv, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &pid, Origin: model.OriginTool, Title: "needs validation", Severity: "medium"})
	_, _ = db.CreateInvestigation(ctx, model.Investigation{ProjectID: pid, ObservationID: inv.ID, Title: "validate"})

	resp, err := http.Get(srv.URL + "/v1/home")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var home struct {
		Projects []struct {
			OpenFindings       int `json:"open_findings"`
			ToTriage           int `json:"to_triage"`
			OpenInvestigations int `json:"open_investigations"`
		} `json:"projects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&home); err != nil {
		t.Fatal(err)
	}
	if len(home.Projects) != 1 {
		t.Fatalf("want 1 project, got %d", len(home.Projects))
	}
	p := home.Projects[0]
	// The finding's observation is confirmed; the open-port and to-validate observations stay unreviewed.
	if p.OpenFindings != 1 {
		t.Errorf("open_findings = %d, want 1", p.OpenFindings)
	}
	if p.ToTriage == 0 {
		t.Errorf("to_triage = %d, want > 0", p.ToTriage)
	}
	if p.OpenInvestigations != 1 {
		t.Errorf("open_investigations = %d, want 1", p.OpenInvestigations)
	}
}
