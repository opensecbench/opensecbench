package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestClassificationLevelsCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// Seeded with the three built-ins.
	levels, err := db.ListClassificationLevels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(levels) != 3 || levels[0].ID != "open_source" || levels[2].ID != "private" {
		t.Fatalf("seeded levels = %+v", levels)
	}
	if !levels[0].Builtin {
		t.Fatal("seeded levels should be builtin")
	}

	// Built-ins can't be deleted.
	if err := db.DeleteClassificationLevel(ctx, "private"); err == nil {
		t.Fatal("deleting a built-in should fail")
	}

	// Add a custom tier between internal and private; the scale sees it.
	if _, err := db.CreateClassificationLevel(ctx, model.ClassificationLevel{ID: "confidential", Label: "Confidential", Rank: 15}); err != nil {
		t.Fatal(err)
	}
	sc := db.LoadScale(ctx)
	if !sc.Has("confidential") || !sc.Allows("private", "confidential") || sc.Allows("internal", "confidential") {
		t.Fatal("custom level not wired into the scale correctly")
	}

	// A custom level in use by a connection can't be deleted.
	if _, err := db.CreateProvider(ctx, model.Provider{Name: "c", Type: "anthropic", DataClearance: "confidential"}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteClassificationLevel(ctx, "confidential"); err == nil {
		t.Fatal("deleting an in-use custom level should fail")
	}

	// An unused custom level deletes cleanly.
	if _, err := db.CreateClassificationLevel(ctx, model.ClassificationLevel{ID: "spare", Label: "Spare", Rank: 5}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteClassificationLevel(ctx, "spare"); err != nil {
		t.Fatalf("deleting an unused custom level should succeed: %v", err)
	}
}
