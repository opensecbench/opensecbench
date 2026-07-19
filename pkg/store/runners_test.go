package store

import (
	"context"
	"testing"
	"time"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestRunnerRegistry(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	r, err := db.CreateRunner(ctx, "edge-1", "cHVia2V5")
	if err != nil {
		t.Fatal(err)
	}
	if r.ID == "" || r.Status != model.RunnerActive || r.LastSeen != nil {
		t.Fatalf("new runner = %+v, want active with no last_seen", r)
	}

	got, err := db.GetRunner(ctx, r.ID)
	if err != nil || got.Name != "edge-1" || got.PubKey != "cHVia2V5" {
		t.Fatalf("GetRunner = %+v err=%v", got, err)
	}

	if err := db.TouchRunner(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := db.GetRunner(ctx, r.ID); got.LastSeen == nil {
		t.Fatal("TouchRunner should stamp last_seen")
	}

	list, _ := db.ListRunners(ctx)
	if len(list) != 1 {
		t.Fatalf("ListRunners = %d, want 1", len(list))
	}

	if err := db.DeleteRunner(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetRunner(ctx, r.ID); err != ErrNotFound {
		t.Fatalf("GetRunner after delete = %v, want ErrNotFound", err)
	}
}

func TestEnrollTokenOneTimeConsume(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	if err := db.MintEnrollToken(ctx, "hash-a", "edge", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// First consume succeeds; a second consume of the same token fails (one-time).
	if ok, err := db.ConsumeEnrollToken(ctx, "hash-a"); err != nil || !ok {
		t.Fatalf("first consume ok=%v err=%v, want true", ok, err)
	}
	if ok, _ := db.ConsumeEnrollToken(ctx, "hash-a"); ok {
		t.Fatal("second consume of the same token should fail")
	}
	// An unknown token never matches.
	if ok, _ := db.ConsumeEnrollToken(ctx, "nope"); ok {
		t.Fatal("unknown token should not consume")
	}
	// An expired token is rejected.
	if err := db.MintEnrollToken(ctx, "hash-exp", "old", time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := db.ConsumeEnrollToken(ctx, "hash-exp"); ok {
		t.Fatal("expired token should not consume")
	}
}
