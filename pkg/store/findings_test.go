package store

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestObservationReviewAndFinding(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, NewTask{CapabilityID: "semgrep", CapabilityVersion: "1.0.0", Actor: "human", Runner: "local-docker"})
	if err != nil {
		t.Fatal(err)
	}

	obs, err := db.CreateObservation(ctx, model.Observation{
		TaskID:   &task.ID,
		Origin:   model.OriginTool,
		Title:    "Hardcoded secret",
		Severity: "high",
		RuleID:   "generic.secrets.security.detected-generic-secret",
		Location: "config.py:12",
	})
	if err != nil {
		t.Fatal(err)
	}
	if obs.ReviewState != model.ReviewUnreviewed {
		t.Fatalf("new observation should be unreviewed, got %s", obs.ReviewState)
	}

	// An unreviewed observation cannot support a finding.
	if _, err := db.CreateFinding(ctx, NewFinding{Title: "Secret", ObservationIDs: []string{obs.ID}}); err == nil {
		t.Fatal("expected error creating finding from unreviewed observation")
	} else if !strings.Contains(err.Error(), "confirmed") {
		t.Fatalf("unexpected error: %v", err)
	}

	// Confirm it, then the finding is allowed.
	if err := db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed); err != nil {
		t.Fatal(err)
	}
	f, err := db.CreateFinding(ctx, NewFinding{
		Title:          "Hardcoded secret in config.py",
		Severity:       "high",
		CWE:            "CWE-798",
		ObservationIDs: []string{obs.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if f.Status != model.FindingOpen {
		t.Fatalf("new finding status = %s, want open", f.Status)
	}

	got, err := db.GetFinding(ctx, f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ObservationIDs) != 1 || got.ObservationIDs[0] != obs.ID {
		t.Fatalf("finding not linked to observation: %+v", got)
	}

	list, err := db.ListFindings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d findings, want 1", len(list))
	}
}

func TestReviewUnknownObservation(t *testing.T) {
	db := migratedDB(t)
	if err := db.ReviewObservation(context.Background(), "nope", model.ReviewConfirmed); err != ErrNotFound {
		t.Fatalf("ReviewObservation(unknown) = %v, want ErrNotFound", err)
	}
}
