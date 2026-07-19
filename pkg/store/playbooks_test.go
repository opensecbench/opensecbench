package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestFailUnfinishedPlaybookRuns(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// Two runs left running (as if the process crashed mid-run) and one already finished.
	r1, _ := db.CreatePlaybookRun(ctx, "recon", nil, "human")
	r2, _ := db.CreatePlaybookRun(ctx, "recon", nil, "human")
	done, _ := db.CreatePlaybookRun(ctx, "recon", nil, "human")
	if err := db.FinishPlaybookRun(ctx, done.ID, model.PlaybookSucceeded); err != nil {
		t.Fatal(err)
	}

	n, err := db.FailUnfinishedPlaybookRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("reconciled %d runs, want 2", n)
	}
	for _, id := range []string{r1.ID, r2.ID} {
		got, _ := db.GetPlaybookRun(ctx, id)
		if got.Status != model.PlaybookFailed || got.FinishedAt == nil {
			t.Fatalf("run %s = %+v, want failed with finished_at set", id, got)
		}
	}
	// The already-finished run is untouched.
	if got, _ := db.GetPlaybookRun(ctx, done.ID); got.Status != model.PlaybookSucceeded {
		t.Fatalf("finished run changed to %s", got.Status)
	}
}
