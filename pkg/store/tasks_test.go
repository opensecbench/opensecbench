package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestTaskAndArtifactProvenance(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	task, err := db.CreateTask(ctx, NewTask{
		CapabilityID:      "semgrep",
		CapabilityVersion: "1.0.0",
		Actor:             "human:james",
		Runner:            "local-docker",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != model.TaskRunning || task.StartedAt == nil {
		t.Fatalf("new task not running/started: %+v", task)
	}

	art, err := db.CreateArtifact(ctx, model.Artifact{
		TaskID: &task.ID,
		SHA256: "abc123",
		Size:   42,
		Kind:   model.ArtifactOutput,
		Name:   "semgrep.sarif",
	})
	if err != nil {
		t.Fatal(err)
	}

	code := 0
	if err := db.FinishTask(ctx, task.ID, model.TaskSucceeded, &code, ""); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.TaskSucceeded || got.ExitCode == nil || *got.ExitCode != 0 || got.FinishedAt == nil {
		t.Fatalf("finished task not recorded correctly: %+v", got)
	}

	arts, err := db.ListArtifactsByTask(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(arts) != 1 || arts[0].ID != art.ID || arts[0].Name != "semgrep.sarif" {
		t.Fatalf("artifact provenance not linked: %+v", arts)
	}
}

func TestFinishUnknownTask(t *testing.T) {
	db := migratedDB(t)
	if err := db.FinishTask(context.Background(), "nope", model.TaskFailed, nil, "x"); err != ErrNotFound {
		t.Fatalf("FinishTask(unknown) = %v, want ErrNotFound", err)
	}
}
