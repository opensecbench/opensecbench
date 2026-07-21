package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// TestContextItemTagsPinned covers the analyst-tags + pin flag added in migration 0058/project-0011:
// they must round-trip through create → get → list, and tags normalize (lowercased, de-duped, trimmed).
func TestContextItemTagsPinned(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	proj, err := db.CreateProject(ctx, NewProject{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	art, err := db.CreateArtifact(ctx, model.Artifact{SHA256: "abc123", Size: 3, Kind: model.ArtifactInput, Name: "note"})
	if err != nil {
		t.Fatal(err)
	}

	created, err := db.CreateContextItem(ctx, model.ContextItem{
		ProjectID:  proj.ID,
		Type:       model.ContextNote,
		Name:       "scope note",
		ArtifactID: art.ID,
		Tags:       []string{"Out-of-Scope", " credentials ", "out-of-scope"}, // mixed case, spaces, dup
		Pinned:     true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := db.GetContextItem(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !got.Pinned {
		t.Error("pinned flag not persisted")
	}
	if len(got.Tags) != 2 || got.Tags[0] != "out-of-scope" || got.Tags[1] != "credentials" {
		t.Errorf("tags not normalized/persisted: %#v", got.Tags)
	}

	list, err := db.ListContextItemsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || !list[0].Pinned || len(list[0].Tags) != 2 {
		t.Errorf("list did not carry tags/pin: %#v", list)
	}
}
